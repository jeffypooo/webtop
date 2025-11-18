//go:build mage
// +build mage

package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
	"github.com/magefile/mage/sh"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Default target to run when none is specified
// If not set, running mage will list available targets
// var Default = Build

type Pi mg.Namespace

var (
	buildDir = "bin/pi"
	binName  = "server"
)

// Starts the server on the Raspberry Pi, using SSH. Blocks until the server exits.
func (Pi) Start(host string, username string) error {
	mg.Deps(mg.F(Pi.Deploy, host, username))
	client, err := sshClient(username, host)
	if err != nil {
		return fmt.Errorf("failed to create SSH client: %w", err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	fmt.Println("--------------------------------")
	fmt.Println("RUNNING SERVER")
	fmt.Println("--------------------------------")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	if err := session.Start("~/webtop/server"); err != nil {
		return fmt.Errorf("failed to start server on host: %w", err)
	}
	// handle signals
	go func() {
		sig := <-sigChan
		fmt.Println("Received signal:", sig)
		session.Signal(ssh.SIGTERM)
		// Give a moment, then force kill if necessary
		<-sigChan
		fmt.Println("Force killing server...")
		session.Signal(ssh.SIGKILL)
		session.Close()
		os.Exit(1)
	}()

	session.Stdout = os.Stdout
	session.Stderr = os.Stderr
	err = session.Wait()
	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			switch exitErr.ExitStatus() {
			case 143:
				fmt.Println("Server exited with SIGTERM")
				return nil
			case 130:
				fmt.Println("Server exited with SIGKILL")
				return nil
			default:
				return fmt.Errorf("server exited with unexpected status %d", exitErr.ExitStatus())
			}
		}
		return fmt.Errorf("failed to wait for server to exit: %w", err)
	}

	return nil
}

// Builds and deploys the server to the Raspberry Pi, using SSH.
// Assumes you have SSH keys setup for the Raspberry Pi.
func (Pi) Deploy(
	host string,
	username string,
) error {
	mg.Deps(Pi.Build)
	connStr := fmt.Sprintf("%s@%s", username, host)
	deployPath := "/home/" + username + "/webtop"
	fmt.Printf("Copying binary via SCP to %s:%s\n", connStr, deployPath)

	// Create the deploy path if it doesn't exist
	err := sh.Run("ssh", connStr, "mkdir -p", deployPath)
	if err != nil {
		return fmt.Errorf("failed to create deploy path on host: %w", err)
	}
	err = sh.Run("scp", filepath.Join(buildDir, binName), fmt.Sprintf("%s:%s/%s", connStr, deployPath, binName))
	if err != nil {
		return fmt.Errorf("failed to deploy to host: %w", err)
	}
	return nil
}

// Builds the server for the Raspberry Pi (linux/arm64)
func (Pi) Build() error {
	fmt.Println("Building...")
	env := map[string]string{
		"GOOS":   "linux",
		"GOARCH": "arm64",
	}
	return sh.RunWithV(env, "go", "build", "-o", filepath.Join(buildDir, binName), "./cmd/server.go")
}

// Cleans up the build directory
func (Pi) Clean() {
	fmt.Println("Cleaning...")
	os.RemoveAll(filepath.Join(buildDir, binName))
}

func sshClient(user, host string) (*ssh.Client, error) {

	var authMethods []ssh.AuthMethod

	// Try to connect to SSH agent
	conn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
	if err == nil {
		agent := agent.NewClient(conn)
		signers, err := agent.Signers()
		if err == nil {
			signers = preferRSASHA2(signers)
			authMethods = append(authMethods, ssh.PublicKeys(signers...))
		}
	}

	if len(authMethods) == 0 {
		fmt.Println("No SSH keys found...")
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Dev only.
	}
	addr := host + ":22"
	fmt.Println("Dialing SSH client to", addr)
	return ssh.Dial("tcp", addr, config)
}

func preferRSASHA2(signers []ssh.Signer) []ssh.Signer {
	var out []ssh.Signer
	for _, signer := range signers {
		if signer.PublicKey().Type() == ssh.KeyAlgoRSA {
			if algSigner, ok := signer.(ssh.AlgorithmSigner); ok {
				if mas, err := ssh.NewSignerWithAlgorithms(
					algSigner,
					[]string{
						ssh.KeyAlgoRSASHA256,
						ssh.KeyAlgoRSASHA512,
						ssh.KeyAlgoRSA,
					},
				); err == nil {
					out = append(out, mas)
					continue
				}
			}
		}
		out = append(out, signer)
	}
	return out
}
