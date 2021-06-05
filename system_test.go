package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	purl "net/url"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	smtp "github.com/emersion/go-smtp"
	"github.com/stretchr/testify/require"
)

type cmd struct {
	in string
}

func newCmd() *cmd                  { return &cmd{} }
func (c *cmd) stdIn(in string) *cmd { c.in = in; return c }
func (c *cmd) runAsync(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if len(c.in) > 0 {
		cmd.Stdin = strings.NewReader(c.in)
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe err=%w", err)
	}
	go func() {
		for {
			ob := make([]byte, 1024)
			bc, err := errPipe.Read(ob)
			if err != nil {
				log.Printf("stderr pipe failed err=%v", err)
				return
			}
			log.Printf(">> stderr: %s\n", ob[:bc])
		}
	}()

	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe err=%w", err)
	}
	go func() {
		for {
			ob := make([]byte, 1024)
			bc, err := outPipe.Read(ob)
			if err != nil {
				log.Printf("stdout pipe failed err=%v", err)
				return
			}
			log.Printf(">> stdout: %s\n", ob[:bc])
		}
	}()

	return cmd.Start()
}

func (c *cmd) run(name string, args ...string) (int, string, string) {
	cmd := exec.Command(name, args...)

	var stdOut, stdErr bytes.Buffer
	cmd.Stdout = &stdOut
	cmd.Stderr = &stdErr

	if len(c.in) > 0 {
		cmd.Stdin = strings.NewReader(c.in)
	}

	_ = cmd.Run()
	status := cmd.ProcessState.Sys().(syscall.WaitStatus)

	strOut := stdOut.String()
	strErr := stdErr.String()

	return status.ExitStatus(), strOut, strErr
}

func build(t *testing.T) {
	var status int

	status, _, _ = newCmd().run("make", "build")
	require.Zero(t, status)

	status, _, _ = newCmd().run("ls", "contact")
	require.Zero(t, status)
}

type TestSMTPBackend struct {
	user, pass string
	sessions   chan *TestSMTPSession
}
type TestSMTPSession struct {
	lastData string
}

func (b *TestSMTPBackend) Login(state *smtp.ConnectionState, username, password string) (smtp.Session, error) {
	if username != b.user || password != b.pass {
		return nil, errors.New("Invalid username or password")
	}
	s := &TestSMTPSession{}
	b.sessions <- s
	return s, nil
}

func (b *TestSMTPBackend) AnonymousLogin(state *smtp.ConnectionState) (smtp.Session, error) {
	return nil, smtp.ErrAuthRequired
}

func (s *TestSMTPSession) Mail(from string, opts smtp.MailOptions) error {
	log.Println(">> test SMTP Mail from:", from)
	return nil
}

func (s *TestSMTPSession) Rcpt(to string) error {
	log.Println(">> test SMTP Rcpt to:", to)
	return nil
}

func (s *TestSMTPSession) Data(r io.Reader) error {
	b, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}

	log.Println(">> test SMTP Data:", string(b))
	s.lastData = string(b)
	return nil
}
func (s *TestSMTPSession) Reset() {}

func (s *TestSMTPSession) Logout() error {
	return nil
}

func startSMTPServer(t *testing.T) *TestSMTPBackend {
	sb := &TestSMTPBackend{
		user:     "test",
		pass:     "abc123",
		sessions: make(chan *TestSMTPSession, 1),
	}
	s := smtp.NewServer(sb)
	s.Addr = ":1025"
	s.Domain = "localhost"
	s.ReadTimeout = 10 * time.Second
	s.WriteTimeout = 10 * time.Second
	s.MaxMessageBytes = 1024 * 1024
	s.MaxRecipients = 50
	s.AllowInsecureAuth = true

	go func() {
		log.Printf("starting test SMTP server\n")
		err := s.ListenAndServe()
		if err != nil {
			log.Printf("Test SMTP server failed err=%v\n", err)
		}
	}()

	return sb
}

func submitTestForm(url, name, email, message string) (*http.Response, error) {
	fd := purl.Values{}
	fd.Add("name", name)
	fd.Add("email", email)
	fd.Add("message", message)
	return http.PostForm(url, fd)
}

func TestSystem(t *testing.T) {
	build(t)
	sb := startSMTPServer(t)

	var err error

	cmd := newCmd()
	err = cmd.runAsync("./contact", "-config", "test-data/test-cfg.yml")
	require.Nil(t, err)
	time.Sleep(100 * time.Millisecond)

	testName, testEmail := "hans", "hans@example.org"
	testMessage := "hello there, system test!"
	resp, err := submitTestForm("http://localhost:5151/mail", testName, testEmail, testMessage)
	require.Nil(t, err)
	require.Equal(t, resp.StatusCode, 200)

	log.Print("waiting for sessions to be available")
	ts := <-sb.sessions

	require.Contains(t, ts.lastData, fmt.Sprintf("Subject: contact from %v", testName))
	require.Contains(t, ts.lastData, fmt.Sprintf("Name: %v", testName))
	require.Contains(t, ts.lastData, fmt.Sprintf("Email: %v", testEmail))
	require.Contains(t, ts.lastData, "hello there")
}
