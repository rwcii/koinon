package core

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// write is one control write through the storage boundary.
func write(ctx context.Context, s *Store) error {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE sessions SET revision=revision WHERE 0"); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

// A request whose context ends during the log proof is refused alone: it proved nothing
// about the log and wrote nothing, so it records no block and the next write proceeds.
func TestEndedContextDoesNotBlockStorage(t *testing.T) {
	s, _ := testStore(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, c := range []struct {
		ctx  context.Context
		want error
	}{{canceled, context.Canceled}, {expired, context.DeadlineExceeded}} {
		if err := write(c.ctx, s); !errors.Is(err, c.want) {
			t.Fatalf("write with an ended context: %v", err)
		}
		if _, err := s.Recover(c.ctx); !errors.Is(err, c.want) {
			t.Fatalf("recovery with an ended context: %v", err)
		}
		if st, err := s.StorageStatus(context.Background()); err != nil || st.Blocked != nil {
			t.Fatalf("an ended context blocked storage: %+v %v", st, err)
		}
		if err := write(context.Background(), s); err != nil {
			t.Fatalf("next write: %v", err)
		}
	}
	// A failed proof under a live context still blocks until recovery.
	real := s.storage.path
	fake := filepath.Join(t.TempDir(), "fake")
	os.WriteFile(fake+"-wal", []byte("unflushed"), 0600)
	s.storage.path = fake
	if err := write(context.Background(), s); code(err) != "storage_blocked" {
		t.Fatalf("failed proof: %v", err)
	}
	s.storage.path = real
	// An ended recovery keeps the block; it does not clear it without a proof.
	if _, err := s.Recover(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("ended recovery: %v", err)
	}
	if err := write(context.Background(), s); code(err) != "storage_blocked" {
		t.Fatalf("block cleared without recovery: %v", err)
	}
	if st, err := s.Recover(context.Background()); err != nil || st.Blocked != nil {
		t.Fatalf("recovery: %+v %v", st, err)
	}
}

// An outside read-only reader, such as a database browser or a backup tool, that holds a
// read transaction while the daemon writes neither blocks nor delays the writes.
func TestOutsideReaderDoesNotBlockStorage(t *testing.T) {
	s, root := testStore(t)
	ctx := context.Background()
	if err := write(ctx, s); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	reader := exec.Command(self, "-test.run=^$")
	reader.Env = append(os.Environ(), "KOINON_TEST_OUTSIDE_READER="+filepath.Join(root, "state.sqlite3"))
	in, err := reader.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := reader.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Start(); err != nil {
		t.Fatal(err)
	}
	defer reader.Process.Kill()
	if line, err := bufio.NewReader(out).ReadString('\n'); err != nil || line != "reading\n" {
		t.Fatalf("outside reader: %q %v", line, err)
	}
	for i := 0; i < 5; i++ {
		bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := write(bounded, s)
		cancel()
		if err != nil {
			t.Fatalf("write %d while a reader holds a read transaction: %v", i, err)
		}
	}
	in.Close()
	if err := reader.Wait(); err != nil {
		t.Fatalf("outside reader: %v", err)
	}
	if st, err := s.StorageStatus(ctx); err != nil || st.Blocked != nil {
		t.Fatalf("storage after the reader: %+v %v", st, err)
	}
	if err := write(ctx, s); err != nil {
		t.Fatalf("write after the reader: %v", err)
	}
}

// outsideReader runs in a separate process: it opens the database read-only, holds one read
// transaction until its standard input closes, and reports when it reads.
func outsideReader(path string) int {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return 1
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 1
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("reading")
	io.Copy(io.Discard, os.Stdin)
	if err := tx.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
