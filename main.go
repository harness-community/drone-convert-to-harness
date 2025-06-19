// Copyright 2023 the Drone Authors. All rights reserved.
// Use of this source code is governed by the Blue Oak Model License
// that can be found in the LICENSE file.

package main

import (
	"github.com/harness-community/drone-convert-to-harness/plugin"
	"github.com/kelseyhightower/envconfig"
	"github.com/sirupsen/logrus"
)

func main() {
	var args plugin.Args
	if err := envconfig.Process("", &args); err != nil {
		logrus.Fatal(err)
	}

	switch args.Level {
	case "debug":
		logrus.SetLevel(logrus.DebugLevel)
	case "trace":
		logrus.SetFormatter(&logrus.TextFormatter{
			DisableColors: true,
			FullTimestamp: true,
		})
		logrus.SetLevel(logrus.TraceLevel)
	}

	if err := plugin.Exec(args); err != nil {
		logrus.Fatal(err)
	}
}
