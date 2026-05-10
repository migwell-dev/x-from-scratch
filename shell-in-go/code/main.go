package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
)

var currDir, hostName string

func execInput(input string) error {
	input = strings.TrimSuffix(input, "\n")

	args := strings.Split(input, " ")

	allDots := true

	for _, ch := range input {
		if ch != '.' {
			allDots = false
			break
		}
	}

	if allDots && len(input) >= 2 {
		levels := len(input) - 1

		var path strings.Builder

		for range levels {
			path.WriteString("../")
		}

		err := os.Chdir(path.String())
		if err != nil {
			return err
		}

		currDir, err = getLowestDir()
		if err != nil {
			currDir = "unknown"
		}

		return nil
	}

	switch args[0] {
	case "cd":
		if len(args) < 2 {
			return errors.New("Path required.")
		}

		err := os.Chdir(args[1])
		if err != nil {
			return err
		}

		currDir, err = getLowestDir()
		if err != nil {
			currDir = "unknown"
		}

		return nil
	case "push":
		if len(args) < 2 {
			return errors.New("commit message required")
		}

		message := strings.Join(args[1:], " ")

		commands := [][]string{
			{"git", "add", "."},
			{"git", "commit", "-m", message},
			{"git", "push"},
		}

		for _, c := range commands {
			cmd := exec.Command(c[0], c[1:]...)

			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin

			err := cmd.Run()
			if err != nil {
				return err
			}
		}

		return nil
	case "exit":
		fmt.Println("Program exited.")
		os.Exit(0)
	}

	cmd := exec.Command(args[0], args[1:]...)

	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	return cmd.Run()
}

func getLowestDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "unknown", err
	}
	dirs := strings.Split(dir, "/")
	return dirs[len(dirs)-1], nil
}

func main() {
	var err error

	hostName, err = os.Hostname()
	if err != nil {
		hostName = "unknown"
	}

	currDir, err = getLowestDir()
	if err != nil {
		currDir = "unknown"
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		_ = <-sigChan
		fmt.Println()
		fmt.Println("Program exited.")
		os.Exit(0)
	}()

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("-> %s: %s ~ ", hostName, currDir)
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}

		if err = execInput(input); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
}
