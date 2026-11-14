package remote

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"
)

// StreamDialer is an Adapter that opens a TCP stream to a tailnet address through
// tailscaled itself. A host whose Tailscale uses no TUN interface (userspace networking,
// common in a container) has no route to tailnet addresses; tailscaled still reaches them
// for its own clients (#688).
type StreamDialer interface {
	DialStream(ctx context.Context, address string) (net.Conn, error)
}

// DialStream opens a stream to address (HOST:PORT) through tailscaled, or reports that the
// adapter cannot.
func (m Manager) DialStream(ctx context.Context, address string) (net.Conn, error) {
	d, ok := m.adapter().(StreamDialer)
	if !ok {
		return nil, diagnostic("tailscale_stream_unavailable", "this Tailscale command cannot open a stream; update Tailscale, then retry")
	}
	return d.DialStream(ctx, address)
}

// DialStream runs `tailscale nc HOST PORT` and uses its standard input and output as the
// connection. A failed connection ends the command, so the first read or write fails, as a
// refused TCP connection does. The command ends when the connection closes.
func (Exec) DialStream(ctx context.Context, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	path, err := tailscalePath()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The command's output is this end's input, and its input is this end's output.
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	// Not CommandContext: the connection outlives the dial's context.
	cmd := exec.Command(path, "nc", host, port)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = outR, inW, io.Discard
	err = cmd.Start()
	outR.Close()
	inW.Close()
	if err != nil {
		inR.Close()
		outW.Close()
		return nil, err
	}
	return &streamConn{r: inR, w: outW, cmd: cmd, remote: streamAddr(address)}, nil
}

// streamConn is a connection over a command's standard input and output. The pipes are
// non-blocking files, so deadlines work as on a socket.
type streamConn struct {
	r, w   *os.File
	cmd    *exec.Cmd
	remote streamAddr
	once   sync.Once
}

func (c *streamConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *streamConn) Write(p []byte) (int, error) { return c.w.Write(p) }

// Close closes both pipes, ends the command and waits for it.
func (c *streamConn) Close() error {
	c.once.Do(func() {
		c.w.Close()
		c.r.Close()
		if c.cmd.Process != nil {
			c.cmd.Process.Kill()
		}
		c.cmd.Wait()
	})
	return nil
}

func (c *streamConn) LocalAddr() net.Addr  { return streamAddr("tailscaled") }
func (c *streamConn) RemoteAddr() net.Addr { return c.remote }

func (c *streamConn) SetDeadline(t time.Time) error {
	if err := c.r.SetReadDeadline(t); err != nil {
		return err
	}
	return c.w.SetWriteDeadline(t)
}
func (c *streamConn) SetReadDeadline(t time.Time) error  { return c.r.SetReadDeadline(t) }
func (c *streamConn) SetWriteDeadline(t time.Time) error { return c.w.SetWriteDeadline(t) }

// streamAddr names an end of a stream through tailscaled.
type streamAddr string

func (a streamAddr) Network() string { return "tailscale-nc" }
func (a streamAddr) String() string  { return string(a) }
