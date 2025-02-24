// Copyright 2014, The Serviced Authors. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package cmd

import (
	"fmt"
	"os"

	"github.com/codegangsta/cli"
)

// Initializer for serviced backup and serviced restore
func (c *ServicedCli) initBackup() {
	c.app.Commands = append(
		c.app.Commands,
		cli.Command{
			Name:        "backup",
			Usage:       "Dump all templates and services to a tgz file",
			Description: "serviced backup DIRPATH",
			Action:      c.cmdBackup,
		},
		cli.Command{
			Name:        "restore",
			Usage:       "Restore templates and services from a tgz file",
			Description: "serviced restore FILEPATH",
			Action:      c.cmdRestore,
		},
	)
}

// serviced backup DIRPATH
func (c *ServicedCli) cmdBackup(ctx *cli.Context) {
	args := ctx.Args()
	if len(args) < 1 {
		fmt.Printf("Incorrect Usage.\n\n")
		cli.ShowCommandHelp(ctx, "backup")
		return
	}

	if path, err := c.driver.Backup(args[0]); err != nil {
		fmt.Fprintln(os.Stderr, err)
	} else if path == "" {
		fmt.Fprintln(os.Stderr, "received nil path to backup file")
	} else {
		fmt.Println(path)
	}
}

// serviced restore FILEPATH
func (c *ServicedCli) cmdRestore(ctx *cli.Context) {
	args := ctx.Args()
	if len(args) < 1 {
		fmt.Printf("Incorrect Usage.\n\n")
		cli.ShowCommandHelp(ctx, "restore")
		return
	}

	err := c.driver.Restore(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
