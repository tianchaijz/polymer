package polymer

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Create a separate goroutine for each TCP connection.
type TcpInput struct {
	keepAliveDuration time.Duration
	listener          net.Listener
	wg                sync.WaitGroup
	sendChan          chan<- string
	stopChan          chan bool
	config            *TcpInputConfig
}

type TcpInputConfig struct {
	// Network type (e.g. "tcp", "tcp4", "tcp6", "unix" or "unixpacket").
	// Needs to match the input type.
	Net string
	// String representation of the address of the network connection on which
	// the listener should be listening (e.g. "127.0.0.1:5565").
	Address string
	// Set to true if TCP Keep Alive should be used.
	KeepAlive bool
	// Integer indicating seconds between keep alives.
	KeepAlivePeriod int
}

func NewTcpInput(addr string, keepalive bool, sendChan chan<- string) (*TcpInput, error) {
	config := &TcpInputConfig{
		Net:             "tcp4",
		Address:         addr,
		KeepAlive:       keepalive,
		KeepAlivePeriod: 7200,
	}

	input := &TcpInput{
		sendChan: sendChan,
	}
	err := input.Init(config)

	return input, err
}

func (t *TcpInput) Init(config interface{}) error {
	var err error
	t.config = config.(*TcpInputConfig)
	address, err := net.ResolveTCPAddr(t.config.Net, t.config.Address)
	if err != nil {
		return fmt.Errorf("ResolveTCPAddress failed: %s\n", err.Error())
	}
	t.listener, err = net.ListenTCP(t.config.Net, address)
	if err != nil {
		return fmt.Errorf("ListenTCP failed: %s\n", err.Error())
	}
	// We're already listening, make sure we clean up if init fails later on.
	closeIt := true
	defer func() {
		if closeIt {
			t.listener.Close()
		}
	}()
	if t.config.KeepAlivePeriod != 0 {
		t.keepAliveDuration = time.Duration(t.config.KeepAlivePeriod) * time.Second
	}
	t.stopChan = make(chan bool)
	closeIt = false
	return nil
}

// Listen on the provided TCP connection, extracting messages from the incoming
// data until the connection is closed or Stop is called on the input.
func (t *TcpInput) handleConnection(conn net.Conn) {
	raddr := conn.RemoteAddr().String()
	fmt.Printf("Accept connection: %s\n", raddr)

	defer func() {
		conn.Close()
		t.wg.Done()
	}()

	reader := bufio.NewReader(conn)

	stopped := false
	for !stopped {
		select {
		case <-t.stopChan:
			stopped = true
		default:
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			msg, err := reader.ReadString('\n')
			if err != nil {
				if neterr, ok := err.(net.Error); ok && neterr.Timeout() {
					// keep the connection open, we are just checking to see if
					// we are shutting down
				} else {
					fmt.Printf("Close connection: %s\n", raddr)
					stopped = true
					break
				}
				continue
			}
			t.sendChan <- msg
		}
	}
}

func (t *TcpInput) Run() error {
	var conn net.Conn
	var e error
	for {
		if conn, e = t.listener.Accept(); e != nil {
			if netErr, ok := e.(net.Error); ok && netErr.Temporary() {
				fmt.Errorf("TCP accept failed: %s", e)
				continue
			} else {
				break
			}
		}
		if t.config.KeepAlive {
			tcpConn, ok := conn.(*net.TCPConn)
			if !ok {
				return errors.New("KeepAlive only supported for TCP Connections.")
			}
			tcpConn.SetKeepAlive(t.config.KeepAlive)
			if t.keepAliveDuration != 0 {
				tcpConn.SetKeepAlivePeriod(t.keepAliveDuration)
			}
		}
		t.wg.Add(1)
		go t.handleConnection(conn)
	}
	t.wg.Wait()
	return nil
}

func (t *TcpInput) Stop() {
	if err := t.listener.Close(); err != nil {
		fmt.Errorf("Error closing listener: %s", err)
	}
	close(t.stopChan)
}
