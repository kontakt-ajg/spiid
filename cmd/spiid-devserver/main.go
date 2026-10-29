// Package main runs a loopback SFTP server for development and integration tests.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/crypto/ssh"

	"spiid/internal/sftptest"
)

func main() {
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		close(stop)
	}()
	err := run(os.Args[1:], os.Stdout, stop)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spiid-devserver:", err)
		os.Exit(1)
	}
}

// run is the testable core of main: it serves until stop is closed.
func run(args []string, stdout io.Writer, stop <-chan struct{}) error {
	fs := flag.NewFlagSet("spiid-devserver", flag.ContinueOnError)
	root := fs.String("root", "", "folder to serve (required)")
	addr := fs.String("addr", "127.0.0.1:2222", "loopback listen address")
	user := fs.String("user", "dev", "user name")
	password := fs.String("password", "dev", "password")
	delay := fs.Duration("read-delay", 0, "sleep before every READ reply")
	knownHosts := fs.String("known-hosts", "", "write the server's known_hosts line to this file")
	sample := fs.Int("make-sample", 0, "create N 50 MiB files sample-<i>.bin and a tree/ folder under root")
	err := fs.Parse(args)
	if err != nil {
		return err
	}
	if *root == "" {
		return errors.New("-root is required")
	}
	if *sample < 0 {
		return errors.New("-make-sample must not be negative")
	}
	err = checkLoopback(*addr)
	if err != nil {
		return err
	}
	if *sample > 0 {
		err = makeSample(*root, *sample)
		if err != nil {
			return err
		}
	}
	s, err := sftptest.Serve(*root, sftptest.Options{User: *user, Password: *password, ReadDelay: *delay}, *addr)
	if err != nil {
		return err
	}
	defer s.Close()
	if *knownHosts != "" {
		err = os.WriteFile(*knownHosts, []byte(s.KnownHostsLine()+"\n"), 0o600)
		if err != nil {
			return err
		}
		err = os.Chmod(*knownHosts, 0o600) // WriteFile keeps the mode of an existing file
		if err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(stdout, "READY addr=%s user=%s fingerprint=%s\n",
		net.JoinHostPort(s.Host(), fmt.Sprint(s.Port())), *user, ssh.FingerprintSHA256(s.HostKey()))
	if err != nil {
		return err
	}
	<-stop
	return nil
}

// checkLoopback accepts only host:port addresses whose host is a literal loopback IP (127.0.0.0/8 or ::1).
// Names such as "localhost" are refused: net.Listen would resolve them a second time.
func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("bad -addr %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("-addr %q must use a literal loopback IP (127.0.0.0/8 or ::1)", addr)
	}
	return nil
}
