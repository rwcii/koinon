package core

import (
	"context"
	"errors"
	"path"
	"reflect"
	"strings"

	"modernc.org/sqlite"
)

// wakePassError adds a fixed operation name without changing errors.Is/As.
type wakePassError struct {
	stage string
	err   error
}

func (e wakePassError) Error() string { return e.stage + ": " + e.err.Error() }
func (e wakePassError) Unwrap() error { return e.err }

type wakeFault struct {
	Class      string   `json:"class"`
	Stage      string   `json:"stage,omitempty"`
	At         int64    `json:"at"`
	ErrorTypes []string `json:"error_types"`
	SQLiteCode *int     `json:"sqlite_code,omitempty"`
}

func wakeErrorTypes(err error) []string {
	var result []string
	queue := []error{err}
	for len(queue) > 0 && len(result) < 8 {
		current := queue[0]
		queue = queue[1:]
		if current == nil {
			continue
		}
		t, prefix := reflect.TypeOf(current), ""
		for t.Kind() == reflect.Pointer {
			prefix += "*"
			t = t.Elem()
		}
		name := t.Name()
		if name == "" {
			// Anonymous types can contain data in struct tags; record their kind only.
			name = t.Kind().String()
		} else {
			name, _, _ = strings.Cut(name, "[")
			if pkg := path.Base(t.PkgPath()); pkg != "." {
				name = pkg + "." + name
			}
		}
		result = append(result, prefix+name)
		switch wrapped := current.(type) {
		case interface{ Unwrap() []error }:
			children := wrapped.Unwrap()
			queue = append(queue, children[:min(len(children), 8)]...)
		case interface{ Unwrap() error }:
			queue = append(queue, wrapped.Unwrap())
		}
	}
	return result
}

func classifyWakeFault(err error, at int64) wakeFault {
	f := wakeFault{Class: "wake_pass_error", At: at, ErrorTypes: wakeErrorTypes(err)}
	var stage wakePassError
	if errors.As(err, &stage) {
		f.Stage = stage.stage
	}
	var dbError *sqlite.Error
	if errors.As(err, &dbError) {
		code := dbError.Code()
		f.SQLiteCode = &code
		f.Class = "sqlite_error"
		switch code & 255 {
		case 5:
			f.Class = "sqlite_busy"
		case 6:
			f.Class = "sqlite_locked"
		case 10:
			f.Class = "sqlite_io"
		case 11, 26:
			f.Class = "sqlite_corrupt"
		case 13:
			f.Class = "sqlite_full"
		case 19:
			f.Class = "sqlite_constraint"
		}
	}
	switch {
	case errors.Is(err, ErrStorageBlocked):
		f.Class = "storage_blocked"
	case errors.Is(err, ErrCapacity):
		f.Class = "capacity"
	case errors.Is(err, context.DeadlineExceeded):
		f.Class = "timeout"
	case errors.Is(err, context.Canceled):
		f.Class = "canceled"
	}
	return f
}

func (f wakeFault) fields() map[string]any {
	fields := map[string]any{"class": f.Class, "stage": f.Stage, "fault_at": f.At,
		"error_types": append([]string(nil), f.ErrorTypes...)}
	if f.SQLiteCode != nil {
		fields["sqlite_code"] = *f.SQLiteCode
	}
	return fields
}

func sameWakeFault(a, b *wakeFault) bool {
	if a == nil {
		return false
	}
	sameCode := a.SQLiteCode == nil && b.SQLiteCode == nil ||
		a.SQLiteCode != nil && b.SQLiteCode != nil && *a.SQLiteCode == *b.SQLiteCode
	return a.Class == b.Class && a.Stage == b.Stage && sameCode &&
		strings.Join(a.ErrorTypes, "\x00") == strings.Join(b.ErrorTypes, "\x00")
}

func (d *Daemon) recordWakePass(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return // Normal daemon shutdown is not a failed pass.
	}
	var event string
	var fields map[string]any
	d.store.wake.mu.Lock()
	if err != nil {
		fault := classifyWakeFault(err, d.store.now().UnixMilli())
		if !sameWakeFault(d.store.wake.currentFault, &fault) {
			event, fields = "wake_fault", fault.fields()
		}
		d.store.wake.fault = fault.Class
		d.store.wake.currentFault, d.store.wake.lastFault = &fault, &fault
	} else if previous := d.store.wake.currentFault; previous != nil {
		event, fields = "wake_recovered", previous.fields()
		d.store.wake.fault, d.store.wake.currentFault = "", nil
	}
	d.store.wake.mu.Unlock()
	if event != "" && d.logEvent != nil {
		d.logEvent(event, fields)
	}
}
