// Copyright 2022 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package binding

import (
	"context"
	"fmt"
	"sync"

	"github.com/openconfig/ondatra/binding"
	"golang.org/x/crypto/ssh"
)

// cli implements the binding.ClientClient interface using SSH. Each command
// runs on its own SSH connection, which is closed when the command completes,
// because Ondatra never closes CLI clients.
type cli struct {
	*binding.AbstractCLIClient
	dial func() (*ssh.Client, error)

	mu    sync.Mutex
	first *ssh.Client // Connection for the first command; nil once taken.
}

// newCLI returns a cli that runs its first command on sc and dials a new
// connection for each later command.
func newCLI(sc *ssh.Client, dial func() (*ssh.Client, error)) (*cli, error) {
	return &cli{dial: dial, first: sc}, nil
}

func (c *cli) conn() (*ssh.Client, error) {
	c.mu.Lock()
	sc := c.first
	c.first = nil
	c.mu.Unlock()
	if sc != nil {
		return sc, nil
	}
	return c.dial()
}

func (c *cli) RunCommand(_ context.Context, cmd string) (binding.CommandResult, error) {
	sc, err := c.conn()
	if err != nil {
		return nil, err
	}
	defer sc.Close()
	sess, err := sc.NewSession()
	if err != nil {
		return nil, fmt.Errorf("could not create session: %w", err)
	}
	defer sess.Close()

	out, err := sess.CombinedOutput(cmd)
	switch err.(type) {
	case nil:
		return &cmdResult{output: string(out)}, nil
	case *ssh.ExitError, *ssh.ExitMissingError:
		return &cmdResult{output: string(out), error: err.Error()}, nil
	default:
		return nil, err
	}
}

type cmdResult struct {
	*binding.AbstractCommandResult
	output, error string
}

func (r *cmdResult) Output() string {
	return r.output
}

func (r *cmdResult) Error() string {
	return r.error
}
