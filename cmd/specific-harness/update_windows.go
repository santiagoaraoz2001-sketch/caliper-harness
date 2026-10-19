package main

import (
	"os"
	"os/exec"
	"os/signal"
	"specific-harness/internal/update"
)

func runSelected(s update.Selection) (int, error) {
	cmd := exec.Command(s.Executable(), os.Args[1:]...)
	cmd.Env = s.Env(os.Environ())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Console Ctrl+C is also delivered to the child. Keep this parent alive
	// while the child cancels its work and saves state, instead of exiting early.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}
