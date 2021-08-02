/*
* Copyright (C) 2020 THL A29 Limited, a Tencent company.  All rights reserved.
* This source code is licensed under the Apache License Version 2.0.
*/

package runner

import (
	"bytes"
	"github.com/pkg/errors"
	"io"
	"nocalhost/pkg/nhctl/log"
	"os"
	"os/exec"
)

var Runner = &CmdRunner{}

type CmdRunner struct{}

func (r *CmdRunner) RunWithCheckResult(cmd *exec.Cmd) error {
	if stdout, stderr, err := r.Run(cmd); err != nil {
		return errors.Errorf(
			"Run command: %s, error: %v, stdout: %s, stderr: %s", cmd.Args, err, stdout, stderr,
		)
	}
	return nil
}

func (r *CmdRunner) CheckResult(cmd *exec.Cmd, stdout string, stderr string, err error) error {
	if err != nil {
		return errors.Errorf(
			"Run command: %s, error: %v, stdout: %s, stderr: %s", cmd.Args, err, stdout, stderr,
		)
	}
	return nil
}

func (r *CmdRunner) RunSimple(cmd *exec.Cmd, stdoutConsumer func(string) error) error {
	stdout, stderr, err := r.Run(cmd)

	if err != nil {
		return err
	}
	if stderr != "" {
		return errors.New(stderr)
	}

	return stdoutConsumer(stdout)
}

func (r *CmdRunner) Run(cmd *exec.Cmd) (string, string, error) {
	log.Infof("Running command: %s", cmd.Args)

	stdout := bytes.Buffer{}
	cmd.Stdout = &stdout
	stderr := bytes.Buffer{}
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return "", "", errors.Errorf("starting command %v: %v", cmd, err)
	}

	if err := cmd.Wait(); err != nil {
		return stdout.String(), stderr.String(), errors.Wrap(err, "")
	}

	if stderr.Len() > 0 {
		log.Infof("Command output: [\n%s\n], stderr: [\n%s\n]", stdout.String(), stderr.String())
	}

	return stdout.String(), stderr.String(), nil
}

func (r *CmdRunner) RunWithRollingOutWithChecker(cmd *exec.Cmd, checker func(log string) bool) (string, string, error) {
	log.Infof("Running command: %s", cmd.Args)
	stdoutBuf := bytes.NewBuffer(make([]byte, 1024))
	stderrBuf := bytes.NewBuffer(make([]byte, 1024))
	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()
	stdout := io.MultiWriter(os.Stdout, stdoutBuf)
	stderr := io.MultiWriter(os.Stderr, stderrBuf)
	go func() {
		_, _ = io.Copy(stdout, stdoutPipe)
	}()
	go func() {
		_, _ = io.Copy(stderr, stderrPipe)
	}()
	go func() {
		if checker != nil {
			for {
				if checker(stdoutBuf.String()) || checker(stderrBuf.String()) {
					break
				}
			}
		}
	}()
	if err := cmd.Start(); err != nil {
		_ = cmd.Process.Kill()
		return stdoutBuf.String(), stderrBuf.String(), err
	}
	_ = cmd.Wait()
	var err error
	if !cmd.ProcessState.Success() {
		err = errors.New("exit code is not 0")
	}
	return stdoutBuf.String(), stderrBuf.String(), err
}
