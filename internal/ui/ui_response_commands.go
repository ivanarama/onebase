package ui

import (
	"context"
	"net/http"
	"sync"
)

// A form response is a second transport for its own committed UI commands.
// The command ID is shared with SSE: whichever arrives first executes once.
// It never carries commands addressed to another login or role.
type uiResponseCommand struct {
	Name      string `json:"name"`
	Data      any    `json:"data"`
	CommandID string `json:"commandId"`
}

type uiResponseCommands struct {
	mu       sync.Mutex
	commands []uiResponseCommand
}
type uiResponseCommandsKey struct{}

func withUIResponseCommands(r *http.Request) *http.Request {
	if r.Header.Get("X-OneBase-UI-Commands") != "1" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), uiResponseCommandsKey{}, &uiResponseCommands{}))
}

func responseCommandsFrom(ctx context.Context) *uiResponseCommands {
	c, _ := ctx.Value(uiResponseCommandsKey{}).(*uiResponseCommands)
	return c
}

func (c *uiResponseCommands) add(command uiResponseCommand) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.commands) < 64 {
		c.commands = append(c.commands, command)
	}
}

func attachUIResponseCommands(r *http.Request, resp *formEventResponse, wrote bool) {
	c := responseCommandsFrom(r.Context())
	if c == nil || !resp.OK || resp.Error != "" || resp.Cancel || resp.Question != nil {
		return
	}
	c.mu.Lock()
	resp.UICommands = append([]uiResponseCommand(nil), c.commands...)
	c.mu.Unlock()
	// The browser uses this only while SSE is unavailable. No extra list GET
	// for a healthy connection, and no business data in the invalidation bit.
	resp.RefreshLive = wrote
}
