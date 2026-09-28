package main

import (
	"context"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestHandlePaste(t *testing.T) {
	ctx := context.Background()
	ch := transport.NewInProcChannel(16)
	cli := client.NewClient(ch, 80, 24, "C-b")
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{{PaneID: 1, Width: 80, Height: 24}},
	})

	rt := &router{prefix: "ctrl+b"}

	// 1. Regular paste forwards via SendPaste with Paste: true
	handlePaste(ctx, cli, rt, uv.PasteEvent{Content: "hello world\n"})
	select {
	case msg := <-ch.ClientSendChan():
		in, ok := msg.(protocol.MsgInput)
		if !ok {
			t.Fatalf("expected MsgInput, got %T", msg)
		}
		if string(in.Data) != "hello world\n" {
			t.Errorf("got data %q, want %q", string(in.Data), "hello world\n")
		}
		if !in.Paste {
			t.Errorf("expected Paste to be true, got false")
		}
	default:
		t.Fatal("expected message on ClientSendChan, got none")
	}

	// 1b. SendInput forwards raw input with Paste: false
	cli.SendInput(ctx, []byte("typed"))
	select {
	case msg := <-ch.ClientSendChan():
		in, ok := msg.(protocol.MsgInput)
		if !ok {
			t.Fatalf("expected MsgInput, got %T", msg)
		}
		if string(in.Data) != "typed" {
			t.Errorf("got data %q, want %q", string(in.Data), "typed")
		}
		if in.Paste {
			t.Errorf("expected Paste to be false for SendInput, got true")
		}
	default:
		t.Fatal("expected message on ClientSendChan, got none")
	}

	// 2. Paste when help is visible is ignored
	rt.help = true
	handlePaste(ctx, cli, rt, uv.PasteEvent{Content: "ignored"})
	select {
	case msg := <-ch.ClientSendChan():
		t.Fatalf("expected 0 messages when help is visible, got %T", msg)
	default:
	}
	rt.help = false

	// 3. Paste during search input routes to search
	cli.StartSearch()
	rt.search = 1
	handlePaste(ctx, cli, rt, uv.PasteEvent{Content: "query"})
	select {
	case msg := <-ch.ClientSendChan():
		t.Fatalf("expected 0 sent messages during search edit, got %T", msg)
	default:
	}
	if got := cli.SearchQuery(); got != "query" {
		t.Errorf("got search query %q, want %q", got, "query")
	}
}
