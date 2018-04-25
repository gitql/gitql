package gitbase

import (
	"sync"
	"time"

	"google.golang.org/grpc/connectivity"
	bblfsh "gopkg.in/bblfsh/client-go.v2"
	errors "gopkg.in/src-d/go-errors.v1"
	"gopkg.in/src-d/go-log.v0"
	"gopkg.in/src-d/go-mysql-server.v0/server"
	"gopkg.in/src-d/go-mysql-server.v0/sql"
	"gopkg.in/src-d/go-vitess.v0/mysql"
)

// Session is the custom implementation of a gitbase session.
type Session struct {
	sql.Session
	Pool *RepositoryPool

	bblfshMu       sync.Mutex
	bblfshEndpoint string
	bblfshClient   *bblfsh.Client

	SkipGitErrors bool
}

const (
	bblfshEndpointKey     = "BBLFSH_ENDPOINT"
	defaultBblfshEndpoint = "127.0.0.1:9432"
)

// SessionOption is a function that configures the session given some options.
type SessionOption func(*Session)

// WithBblfshEndpoint configures the bblfsh endpoint of the session.
func WithBblfshEndpoint(endpoint string) SessionOption {
	return func(s *Session) {
		s.bblfshEndpoint = endpoint
	}
}

// WithSkipGitErrors changes the behavior with go-git error.
func WithSkipGitErrors(enabled bool) SessionOption {
	return func(s *Session) {
		s.SkipGitErrors = enabled
	}
}

// NewSession creates a new Session. It requires a repository pool and any
// number of session options can be passed to configure the session.
func NewSession(pool *RepositoryPool, opts ...SessionOption) *Session {
	sess := &Session{
		Session:        sql.NewBaseSession(),
		Pool:           pool,
		bblfshEndpoint: getStringEnv(bblfshEndpointKey, defaultBblfshEndpoint),
	}

	for _, opt := range opts {
		opt(sess)
	}

	return sess
}

const bblfshMaxAttempts = 10

// BblfshClient returns a BblfshClient.
func (s *Session) BblfshClient() (*bblfsh.Client, error) {
	var err error
	s.bblfshMu.Lock()
	defer s.bblfshMu.Unlock()

	if s.bblfshClient == nil {
		s.bblfshClient, err = bblfsh.NewClient(s.bblfshEndpoint)
		if err != nil {
			return nil, err
		}
	}

	logger, _ := log.New()

	var attempts, totalAttempts int
	for {
		if attempts > bblfshMaxAttempts || totalAttempts > 3*bblfshMaxAttempts {
			return nil, ErrBblfshConnection.New()
		}

		switch s.bblfshClient.GetState() {
		case connectivity.Ready, connectivity.Idle:
			return s.bblfshClient, nil
		case connectivity.Connecting:
			attempts = 0
			logger.New(log.Fields{"attempts": totalAttempts}).
				Debugf("bblfsh is connecting, sleeping 100ms")
			time.Sleep(100 * time.Millisecond)
		default:
			if err := s.bblfshClient.Close(); err != nil {
				return nil, err
			}

			logger.Debugf("bblfsh connection is closed, opening a new one")

			s.bblfshClient, err = bblfsh.NewClient(s.bblfshEndpoint)
			if err != nil {
				return nil, err
			}
		}

		attempts++
		totalAttempts++
	}
}

// Close implements the io.Closer interface.
func (s *Session) Close() error {
	s.bblfshMu.Lock()
	defer s.bblfshMu.Unlock()

	if s.bblfshClient != nil {
		return s.bblfshClient.Close()
	}
	return nil
}

// NewSessionBuilder creates a SessionBuilder with the given Repository Pool.
func NewSessionBuilder(pool *RepositoryPool, opts ...SessionOption) server.SessionBuilder {
	return func(_ *mysql.Conn) sql.Session {
		return NewSession(pool, opts...)
	}
}

// ErrSessionCanceled is returned when session context is canceled
var ErrSessionCanceled = errors.NewKind("session canceled")

// ErrInvalidGitbaseSession is returned when some node expected a gitbase
// session but received something else.
var ErrInvalidGitbaseSession = errors.NewKind("expecting gitbase session, but received: %T")

// ErrInvalidContext is returned when some node expected an sql.Context
// with gitbase session but received something else.
var ErrInvalidContext = errors.NewKind("invalid context received: %v")

// ErrBblfshConnection is returned when it's impossible to connect to bblfsh.
var ErrBblfshConnection = errors.NewKind("unable to establish a new bblfsh connection")
