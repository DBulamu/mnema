// Package system provides production adapters for ambient capabilities
// (clock, randomness) that usecases consume via ports.
//
// Production wires these; tests substitute fakes that pin time and
// randomness — the explicit indirection is what makes business logic
// deterministic under test.
package system

import "time"

// Clock returns the wall-clock current time, in UTC. UTC is mandatory:
// timestamps cross process / DB / JWT boundaries where DST shifts are
// invariably bugs.
type Clock struct{}

func (Clock) Now() time.Time { return time.Now().UTC() }
