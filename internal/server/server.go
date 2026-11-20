// Package server binds and runs the HTTP listeners of the serve command.
//
// Bind opens every socket and loads every certificate up front, so that a
// port in use or a bad certificate is a start-up error rather than something
// found while serving. Serve then runs all listeners until its context is
// cancelled or one of them fails.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"
)

var (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 15 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 5 * time.Second
)

// maxHeaderBytes bounds the request line and headers of every request. The
// APIs need far less, and a smaller bound keeps oversized paths out of the
// handlers and history.
const maxHeaderBytes = 16 << 10

// Listener describes one socket to serve. TLSCert and TLSKey are paths to a
// PEM certificate and key; both empty means plain HTTP.
type Listener struct {
	Name            string
	Addr            netip.AddrPort
	Handler         http.Handler
	TLSCert, TLSKey string
}

// Server holds bound listeners ready to be served. It is made by Bind and
// served once by Serve.
type Server struct {
	bound []bound
}

type bound struct {
	name     string
	listener net.Listener
	server   *http.Server
	tls      bool
}

// Bind opens every listener and loads any certificate. On any failure it
// closes what it opened and returns an error naming the listener. Listener
// names must be unique.
func Bind(listeners []Listener) (*Server, error) {
	s := &Server{}
	seen := make(map[string]bool, len(listeners))
	for _, l := range listeners {
		if seen[l.Name] {
			s.closeAll()
			return nil, fmt.Errorf("listener %s: duplicate name", l.Name)
		}
		seen[l.Name] = true
		b, err := bind(l)
		if err != nil {
			s.closeAll()
			return nil, fmt.Errorf("listener %s: %w", l.Name, err)
		}
		s.bound = append(s.bound, b)
	}
	return s, nil
}

func bind(l Listener) (bound, error) {
	srv := &http.Server{
		Handler:           l.Handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
	useTLS := l.TLSCert != "" || l.TLSKey != ""
	if useTLS {
		if l.TLSCert == "" || l.TLSKey == "" {
			return bound{}, errors.New("TLS needs both a certificate and a key")
		}
		cert, err := tls.LoadX509KeyPair(l.TLSCert, l.TLSKey)
		if err != nil {
			return bound{}, fmt.Errorf("load certificate: %w", err)
		}
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	}
	ln, err := net.Listen("tcp", l.Addr.String())
	if err != nil {
		return bound{}, err
	}
	return bound{name: l.Name, listener: ln, server: srv, tls: useTLS}, nil
}

func (s *Server) closeAll() {
	for _, b := range s.bound {
		_ = b.listener.Close()
	}
}

// Addr returns the address the named listener is bound to, which tells the
// port chosen for a zero port. It returns nil for an unknown name.
func (s *Server) Addr(name string) net.Addr {
	for _, b := range s.bound {
		if b.name == name {
			return b.listener.Addr()
		}
	}
	return nil
}

// Serve runs until ctx is cancelled, then shuts down gracefully (five
// seconds) and returns nil. If a listener fails first, the others are shut
// down and its error is returned. Requests still running when the five
// seconds are up are cut off. Serve returns only once every listener has
// stopped.
func (s *Server) Serve(ctx context.Context, logger *slog.Logger) error {
	failed := make(chan error, len(s.bound))
	var wg sync.WaitGroup
	for _, b := range s.bound {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.serve(logger); err != nil {
				failed <- fmt.Errorf("listener %s: %w", b.name, err)
			}
		}()
	}

	var result error
	select {
	case <-ctx.Done():
		logger.Info("shutting down", "grace", shutdownTimeout)
	case result = <-failed:
		logger.Error("listener failed, shutting down", "err", result)
	}
	s.shutdown(context.WithoutCancel(ctx), logger)
	wg.Wait()
	return result
}

func (b bound) serve(logger *slog.Logger) error {
	scheme := "http"
	if b.tls {
		scheme = "https"
	}
	logger.Info("listening", "listener", b.name, "addr", b.listener.Addr().String(), "scheme", scheme)
	var err error
	if b.tls {
		err = b.server.ServeTLS(b.listener, "", "")
	} else {
		err = b.server.Serve(b.listener)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) shutdown(ctx context.Context, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, shutdownTimeout)
	defer cancel()
	var wg sync.WaitGroup
	for _, b := range s.bound {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.server.Shutdown(ctx); err != nil {
				logger.Warn("graceful shutdown cut short", "listener", b.name, "err", err)
				_ = b.server.Close()
			}
		}()
	}
	wg.Wait()
}
