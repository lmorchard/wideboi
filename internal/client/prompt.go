package client

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/lmorchard/wideboi/internal/commands"
)

type promptState struct {
	query string
}

// InPrompt reports whether the command prompt is active.
func (c *Client) InPrompt() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.prompt != nil
}

// StartPrompt enters command prompt mode.
func (c *Client) StartPrompt() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prompt = &promptState{}
}

// PromptEdit updates the prompt input string.
func (c *Client) PromptEdit(text string, backspace bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.prompt; s != nil {
		if backspace && s.query != "" {
			_, n := utf8.DecodeLastRuneInString(s.query)
			s.query = s.query[:len(s.query)-n]
		} else if !backspace {
			s.query += text
		}
	}
}

// PromptQuery returns the current prompt input.
func (c *Client) PromptQuery() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.prompt == nil {
		return ""
	}
	return c.prompt.query
}

// PromptCommit executes the entered prompt command line.
func (c *Client) PromptCommit(ctx context.Context, inv commands.Invocation) error {
	c.mu.Lock()
	if c.prompt == nil {
		c.mu.Unlock()
		return nil
	}
	line := c.prompt.query
	c.prompt = nil
	if inv.CallerPaneID == 0 {
		inv.CallerPaneID = c.focusPaneID
	}
	c.mu.Unlock()

	if line == "" {
		return nil
	}
	return commands.DefaultRegistry.Execute(ctx, inv, line)
}

// PromptCancel exits command prompt mode.
func (c *Client) PromptCancel() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prompt = nil
}

func (c *Client) promptStatusLocked() string {
	if c.prompt == nil {
		return ""
	}
	return fmt.Sprintf(": %s_  Enter run · Esc cancel", c.prompt.query)
}
