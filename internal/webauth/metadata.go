package webauth

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

type Metadata struct {
	SourceIP, RequestID string
}

type metadataKey struct{}

func (m *Manager) Transport(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		meta := m.Metadata(r)
		w.Header().Set("X-Request-ID", meta.RequestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), metadataKey{}, meta)))
	})
}

func (m *Manager) Metadata(r *http.Request) Metadata {
	if value, ok := r.Context().Value(metadataKey{}).(Metadata); ok {
		return value
	}
	value := Metadata{RequestID: platform.ID()}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	address, err := netip.ParseAddr(peer)
	if err != nil {
		return value
	}
	value.SourceIP = address.Unmap().String()
	if !m.trusted(address) {
		return value
	}
	chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if len(chain) > 16 {
		return value
	}
	for i := len(chain) - 1; i >= 0; i-- {
		forwarded, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
		if err != nil {
			return Metadata{SourceIP: address.Unmap().String(), RequestID: value.RequestID}
		}
		value.SourceIP = forwarded.Unmap().String()
		if !m.trusted(forwarded) {
			break
		}
	}
	return value
}

func (m *Manager) trusted(address netip.Addr) bool {
	for _, prefix := range m.proxies {
		if prefix.Contains(address.Unmap()) {
			return true
		}
	}
	return false
}

func (m Metadata) Event(actor, project, device, action, result string) auditlog.Event {
	return auditlog.Event{Actor: actor, Project: project, Device: device, Action: action, Result: result, SourceIP: m.SourceIP, RequestID: m.RequestID}
}

// Production writes require an exact configured origin or a same-origin Referer.
// A present invalid Origin is never rescued by a valid Referer.
func (m *Manager) SameOrigin(r *http.Request, required bool) bool {
	if values, present := r.Header["Origin"]; present {
		return len(values) == 1 && values[0] == m.config.PublicURL
	}
	if referer := r.Header.Get("Referer"); referer != "" {
		u, err := url.Parse(referer)
		return err == nil && u.User == nil && u.Scheme+"://"+u.Host == m.config.PublicURL
	}
	return !required
}
