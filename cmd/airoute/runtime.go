package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/zbss/airoute/internal/config"
)

const (
	runtimeStateName = "runtime.json"
	runtimeLogName   = "airoute.log"
)

type runtimeState struct {
	PID        int       `json:"pid"`
	ConfigPath string    `json:"config_path"`
	GatewayURL string    `json:"gateway_url"`
	AdminURL   string    `json:"admin_url,omitempty"`
	LogFile    string    `json:"log_file"`
	StartedAt  time.Time `json:"started_at"`
}

func start(args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	path := fs.String("config", "airoute.yaml", "configuration file")
	foreground := fs.Bool("foreground", false, "run in the foreground")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *foreground {
		return serve([]string{"--config", *path})
	}
	return startBackground(*path)
}

func startBackground(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve configuration path: %w", err)
	}
	c, err := config.Load(absPath)
	if err != nil {
		return err
	}

	statePath, logPath, err := runtimePaths()
	if err != nil {
		return err
	}
	if current, stateErr := loadRuntimeState(statePath); stateErr == nil {
		if processAlive(current.PID) {
			return fmt.Errorf("AI Router is already running (PID %d, config %s)", current.PID, current.ConfigPath)
		}
		_ = os.Remove(statePath)
	} else if !errors.Is(stateErr, os.ErrNotExist) {
		return stateErr
	}
	if err = ensureListenAvailable(c.Server.Listen, "gateway"); err != nil {
		return err
	}
	if c.Admin.Enabled {
		if err = ensureListenAvailable(c.Server.AdminListen, "Web console"); err != nil {
			return err
		}
	}

	if err = rotateRuntimeLog(logPath); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open runtime log: %w", err)
	}

	executable, err := os.Executable()
	if err != nil {
		_ = logFile.Close()
		return fmt.Errorf("resolve executable: %w", err)
	}
	cmd := exec.Command(executable, "serve", "--config", absPath, "--runtime-state", statePath)
	cmd.Dir = filepath.Dir(absPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()
	prepareBackgroundCommand(cmd)
	if err = cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start background process: %w", err)
	}
	pid := cmd.Process.Pid
	_ = logFile.Close()
	_ = cmd.Process.Release()

	state := runtimeState{
		PID:        pid,
		ConfigPath: absPath,
		GatewayURL: "http://" + externalHost(c.Server.Listen),
		LogFile:    logPath,
		StartedAt:  time.Now(),
	}
	if c.Admin.Enabled {
		state.AdminURL = "http://" + externalHost(c.Server.AdminListen)
	}
	if err = saveRuntimeState(statePath, state); err != nil {
		_ = terminateProcess(pid, true)
		return err
	}
	if err = waitForGateway(state.GatewayURL, pid, 8*time.Second); err != nil {
		_ = terminateProcess(pid, true)
		_ = os.Remove(statePath)
		return fmt.Errorf("AI Router did not start: %w (see %s)", err, logPath)
	}

	fmt.Printf("AI Router started in the background (PID %d)\n", pid)
	fmt.Printf("Gateway: %s\n", state.GatewayURL)
	if state.AdminURL != "" {
		fmt.Printf("Web console: %s\n", state.AdminURL)
	}
	fmt.Println("Logs: air logs --follow")
	fmt.Println("Stop: air stop")
	return nil
}

func stop(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	timeout := fs.Duration("timeout", 10*time.Second, "graceful shutdown timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return stopBackground(*timeout, false)
}

func stopBackground(timeout time.Duration, quiet bool) error {
	statePath, _, err := runtimePaths()
	if err != nil {
		return err
	}
	state, err := loadRuntimeState(statePath)
	if errors.Is(err, os.ErrNotExist) {
		if !quiet {
			fmt.Println("AI Router is not running")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !processAlive(state.PID) {
		_ = os.Remove(statePath)
		if !quiet {
			fmt.Println("AI Router is not running (removed stale runtime state)")
		}
		return nil
	}

	_ = terminateProcess(state.PID, false)
	deadline := time.Now().Add(timeout)
	for processAlive(state.PID) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if processAlive(state.PID) {
		if err = terminateProcess(state.PID, true); err != nil {
			return fmt.Errorf("force stop PID %d: %w", state.PID, err)
		}
	}
	_ = os.Remove(statePath)
	if !quiet {
		fmt.Printf("AI Router stopped (PID %d)\n", state.PID)
	}
	return nil
}

func restart(args []string) error {
	fs := flag.NewFlagSet("restart", flag.ContinueOnError)
	path := fs.String("config", "", "configuration file; defaults to the running instance")
	if err := fs.Parse(args); err != nil {
		return err
	}
	statePath, _, err := runtimePaths()
	if err != nil {
		return err
	}
	if *path == "" {
		if state, stateErr := loadRuntimeState(statePath); stateErr == nil {
			*path = state.ConfigPath
		} else {
			*path = "airoute.yaml"
		}
	}
	if err = stopBackground(10*time.Second, true); err != nil {
		return err
	}
	return startBackground(*path)
}

func logs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	lines := fs.Int("lines", 100, "number of recent lines")
	follow := fs.Bool("follow", false, "follow new log output")
	followShort := fs.Bool("f", false, "follow new log output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *lines < 0 {
		return fmt.Errorf("lines must be zero or greater")
	}
	statePath, defaultLog, err := runtimePaths()
	if err != nil {
		return err
	}
	logPath := defaultLog
	if state, stateErr := loadRuntimeState(statePath); stateErr == nil && state.LogFile != "" {
		logPath = state.LogFile
	}
	if err = printLogTail(logPath, *lines); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no runtime log found; start AI Router first")
		}
		return err
	}
	if !*follow && !*followShort {
		return nil
	}
	return followLog(logPath)
}

func runtimePaths() (statePath, logPath string, err error) {
	dir := os.Getenv("AIROUTE_RUNTIME_DIR")
	if dir == "" {
		dir, err = os.UserConfigDir()
		if err != nil {
			return "", "", fmt.Errorf("resolve user configuration directory: %w", err)
		}
		dir = filepath.Join(dir, "airoute")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", "", fmt.Errorf("create runtime directory: %w", err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return "", "", fmt.Errorf("secure runtime directory: %w", err)
	}
	return filepath.Join(dir, runtimeStateName), filepath.Join(dir, runtimeLogName), nil
}

func loadRuntimeState(path string) (runtimeState, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return runtimeState{}, err
	}
	var state runtimeState
	if err = json.Unmarshal(raw, &state); err != nil {
		return runtimeState{}, fmt.Errorf("decode runtime state: %w", err)
	}
	if state.PID <= 0 || state.ConfigPath == "" {
		return runtimeState{}, fmt.Errorf("runtime state is invalid")
	}
	return state, nil
}

func saveRuntimeState(path string, state runtimeState) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err = os.WriteFile(temporary, append(raw, '\n'), 0600); err != nil {
		return fmt.Errorf("write runtime state: %w", err)
	}
	if err = os.Chmod(temporary, 0600); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err = os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("install runtime state: %w", err)
	}
	return nil
}

func removeRuntimeState(path string, pid int) {
	state, err := loadRuntimeState(path)
	if err == nil && state.PID == pid {
		_ = os.Remove(path)
	}
}

func rotateRuntimeLog(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() < 10<<20 {
		return nil
	}
	_ = os.Remove(path + ".1")
	if err = os.Rename(path, path+".1"); err != nil {
		return fmt.Errorf("rotate runtime log: %w", err)
	}
	return nil
}

func waitForGateway(baseURL string, pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	var lastErr error
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return fmt.Errorf("process %d exited", pid)
		}
		resp, err := client.Get(strings.TrimRight(baseURL, "/") + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("health check returned HTTP %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("health check timed out")
	}
	return lastErr
}

func ensureListenAvailable(address, label string) error {
	connection, err := net.DialTimeout("tcp", externalHost(address), 250*time.Millisecond)
	if err != nil {
		return nil
	}
	_ = connection.Close()
	return fmt.Errorf("%s address %s is already in use", label, address)
}

func printLogTail(path string, lineCount int) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	start := info.Size() - (1 << 20)
	if start < 0 {
		start = 0
	}
	if _, err = file.Seek(start, io.SeekStart); err != nil {
		return err
	}
	raw, err := io.ReadAll(file)
	if err != nil {
		return err
	}
	if start > 0 {
		if newline := strings.IndexByte(string(raw), '\n'); newline >= 0 {
			raw = raw[newline+1:]
		}
	}
	entries := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(entries) == 1 && entries[0] == "" {
		return nil
	}
	if lineCount < len(entries) {
		entries = entries[len(entries)-lineCount:]
	}
	if lineCount > 0 {
		fmt.Println(strings.Join(entries, "\n"))
	}
	return nil
}

func followLog(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	offset, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	defer signal.Stop(interrupt)
	for {
		select {
		case <-interrupt:
			return nil
		case <-ticker.C:
			info, statErr := file.Stat()
			if statErr != nil {
				return statErr
			}
			if info.Size() < offset {
				offset = 0
			}
			if info.Size() == offset {
				continue
			}
			if _, err = file.Seek(offset, io.SeekStart); err != nil {
				return err
			}
			written, copyErr := io.Copy(os.Stdout, file)
			offset += written
			if copyErr != nil {
				return copyErr
			}
		}
	}
}
