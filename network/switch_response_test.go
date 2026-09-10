package network

import (
	"bufio"
	"github.com/stretchr/testify/require"
	"io"
	"net"
	"testing"
	"time"
)

func TestSwitchRejectsIOSFailures(t *testing.T) {
	for _, test := range []struct {
		name, output string
		fails        bool
	}{
		{"success", "Switch#\n%SYS-5-CONFIG_I: Configured from console\n", false},
		{"syntax error", "% Invalid input detected at '^' marker.\n", true},
		{"password rejected", "% Bad passwords\n", true},
		{"no password", "Password required, but none set\n", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				reader := bufio.NewReader(conn)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if line == "exit\n" {
						break
					}
				}
				io.WriteString(conn, test.output)
			}()
			sw := NewSwitch("127.0.0.1", "password")
			sw.port = listener.Addr().(*net.TCPAddr).Port
			_, err = sw.runConfigCommand("interface Vlan10\nno ip address\n")
			if test.fails {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			<-done
		})
	}
}

func TestSwitchConnectionHasDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		<-release
	}()
	sw := NewSwitch("127.0.0.1", "password")
	sw.port = listener.Addr().(*net.TCPAddr).Port
	sw.commandTimeout = 25 * time.Millisecond
	_, err = sw.runConfigCommand("interface Vlan10\nno ip address\n")
	close(release)
	<-done
	var timeout net.Error
	require.ErrorAs(t, err, &timeout)
	require.True(t, timeout.Timeout())
}
