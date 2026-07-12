package transport

import "errors"

var ErrNoSession = errors.New("no authenticated tunnel transport is available")

type Session interface {
	Kind() string
	Send([]byte) error
	Receive() <-chan []byte
	Done() <-chan error
	Close() error
}
