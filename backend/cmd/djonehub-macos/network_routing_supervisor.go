package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type routingSupervisorOptions struct {
	Enabled    bool
	ParentPID  int
	CorePath   string
	ConfigPath string
	Control    string
	LogPath    string
	UserUID    int
	UserGID    int
	UserHome   string
}

func registerRoutingSupervisorFlags() *routingSupervisorOptions {
	options := &routingSupervisorOptions{UserUID: -1, UserGID: -1}
	flag.BoolVar(&options.Enabled, "routing-supervisor", false, "run the privileged routing supervisor")
	flag.IntVar(&options.ParentPID, "routing-parent-pid", 0, "parent backend process ID")
	flag.StringVar(&options.CorePath, "routing-core", "", "managed network core path")
	flag.StringVar(&options.ConfigPath, "routing-config", "", "managed network core config path")
	flag.StringVar(&options.Control, "routing-control", "", "routing supervisor control socket")
	flag.StringVar(&options.LogPath, "routing-log", "", "routing core log path")
	flag.IntVar(&options.UserUID, "routing-user-uid", -1, "owning user ID")
	flag.IntVar(&options.UserGID, "routing-user-gid", -1, "owning group ID")
	flag.StringVar(&options.UserHome, "routing-user-home", "", "owning user home")
	return options
}

func runRoutingSupervisor(options routingSupervisorOptions) error {
	if os.Geteuid() != 0 {
		return errors.New("routing supervisor requires administrator privileges")
	}
	if options.ParentPID <= 1 || options.UserUID < 0 || options.UserGID < 0 {
		return errors.New("invalid routing supervisor process metadata")
	}
	for label, path := range map[string]string{
		"core": options.CorePath, "config": options.ConfigPath,
		"control": options.Control, "log": options.LogPath,
	} {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("routing supervisor %s path must be absolute", label)
		}
	}
	if info, err := os.Stat(options.CorePath); err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return errors.New("managed network core is missing or not executable")
	}
	if info, err := os.Stat(options.ConfigPath); err != nil || !info.Mode().IsRegular() {
		return errors.New("managed network core config is missing")
	}
	if err := syscall.Kill(options.ParentPID, 0); err != nil {
		return errors.New("parent backend process is no longer running")
	}

	_ = os.Remove(options.Control)
	listener, err := net.Listen("unix", options.Control)
	if err != nil {
		return fmt.Errorf("listen on routing control socket: %w", err)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(options.Control)
	}()
	if err := os.Chmod(options.Control, 0o600); err != nil {
		return fmt.Errorf("secure routing control socket: %w", err)
	}
	if err := os.Chown(options.Control, options.UserUID, options.UserGID); err != nil {
		return fmt.Errorf("assign routing control socket: %w", err)
	}

	logFile, err := os.OpenFile(options.LogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open routing core log: %w", err)
	}
	defer logFile.Close()
	_ = os.Chown(options.LogPath, options.UserUID, options.UserGID)

	command := exec.Command(options.CorePath, "run", "--disable-color", "-D", filepath.Dir(options.ConfigPath), "-c", options.ConfigPath)
	command.Env = append(os.Environ(), "HOME="+options.UserHome)
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start managed network core: %w", err)
	}

	coreDone := make(chan error, 1)
	go func() { coreDone <- command.Wait() }()
	stopRequested := make(chan string, 1)
	go serveRoutingSupervisorControl(listener, command.Process.Pid, stopRequested)
	go monitorRoutingParent(options.ParentPID, stopRequested)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	coreExited := false
	var exitError error
	select {
	case exitError = <-coreDone:
		coreExited = true
	case <-stopRequested:
	case <-signals:
	}

	if !coreExited {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
		select {
		case <-coreDone:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			select {
			case <-coreDone:
			case <-time.After(time.Second):
			}
		}
		return nil
	}
	if exitError != nil {
		_, _ = fmt.Fprintf(logFile, "\nDJOneHub routing supervisor: network core exited: %v\n", exitError)
		return exitError
	}
	return nil
}

func serveRoutingSupervisorControl(listener net.Listener, corePID int, stopRequested chan<- string) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer connection.Close()
			_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
			line, err := bufio.NewReader(connection).ReadString('\n')
			if err != nil {
				_, _ = fmt.Fprintln(connection, "ERROR invalid request")
				return
			}
			switch strings.ToUpper(strings.TrimSpace(line)) {
			case "STATUS":
				_, _ = fmt.Fprintln(connection, "OK "+strconv.Itoa(corePID))
			case "STOP":
				_, _ = fmt.Fprintln(connection, "OK")
				select {
				case stopRequested <- "control":
				default:
				}
			default:
				_, _ = fmt.Fprintln(connection, "ERROR unsupported command")
			}
		}()
	}
}

func monitorRoutingParent(parentPID int, stopRequested chan<- string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if err := syscall.Kill(parentPID, 0); err == nil || errors.Is(err, syscall.EPERM) {
			continue
		}
		select {
		case stopRequested <- "parent-exited":
		default:
		}
		return
	}
}
