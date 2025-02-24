// Copyright 2014, The Serviced Authors. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package datastore

import (
	"fmt"
)

// ErrNoSuchEntity is returned when no entity was found for a given key.
type ErrNoSuchEntity struct {
	Key Key
}

func (e ErrNoSuchEntity) Error() string {
	return fmt.Sprintf("No such entity {kind:%s, id:%s}", e.Key.Kind(), e.Key.ID())
}

//IsErrNoSuchEntity check see if error param is of type ErrNoSuchEntity
func IsErrNoSuchEntity(err error) bool {
	switch err.(type) {
	case ErrNoSuchEntity:
		return true
	}
	return false
}
