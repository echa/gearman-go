// Copyright 2011 Xing Xing <mikespook@gmail.com> All rights reserved.
// Use of this source code is governed by a MIT
// license that can be found in the LICENSE file.

// TLS and Context extensions from Alexander Eichhorn <alex@kidtsunami.com>

/*
This module is a Gearman API for the Go Programming Language.
The protocols were written in pure Go. It contains two sub-packages:

The client package is used for sending jobs to the Gearman job server,
and getting responses from the server.

	import "github.com/echa/gearman-go/client"

The worker package will help developers to develop Gearman's worker
in an easy way.

	import "github.com/echa/gearman-go/worker"
*/
package gearman

import (
	_ "github.com/echa/gearman-go/client"
	_ "github.com/echa/gearman-go/worker"
)
