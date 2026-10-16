package pipeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// The input of a chat run (IMP-191), as the chat plugin starts one with
// the run token of the bot's pipeline, or as a test by hand:
//
//	kind     message (the default), rating or handoff
//	message  the question (a message), or what the person adds (a handoff)
//	history  the turns before, [{role: user|assistant, content}]
//	scopes   the scopes the person chose among those the bot opens
//	session  the conversation, an id of the plugin's
//	user     who chats: the account of a signed token, or empty
//	turn     the answer rated (a rating): the id of its run
//	rating   the rating, -10 to 10 (a rating)
//	comment  what the person wrote with it (a rating)
//	contact  how a person may answer: an address, a number (a handoff)
//	as_of    the day the answer is about, YYYY-MM-DD (a message)
//
// Other keys pass as they are: a plugin may add its own.
const (
	ChatMessage = "message"
	ChatRating  = "rating"
	// ChatHandoff asks for a person: the pipeline hands the conversation
	// on (a note, a webhook) and says so.
	ChatHandoff = "handoff"
)

// Limits of a chat input.
const (
	maxChatHistory = 200
	maxChatScopes  = 100
	maxChatID      = 200
	maxChatContact = 200
	maxChatNote    = 4000
)

// CheckChatInput checks a chat run's input and gives it with its kind
// set.
func CheckChatInput(input any) (map[string]any, error) {
	in, ok := input.(map[string]any)
	if !ok {
		return nil, errors.New("a chat run's input is an object: message, history, scopes, session, user (or kind: rating, turn, rating)")
	}
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	kind := ChatMessage
	if v, ok := in["kind"]; ok && v != nil {
		k, ok := v.(string)
		if !ok {
			return nil, errors.New("kind: message, rating or handoff")
		}
		if k != "" {
			kind = k
		}
	}
	out["kind"] = kind
	for _, k := range []string{"session", "user"} {
		if err := optionalString(in, k, maxChatID); err != nil {
			return nil, err
		}
	}
	switch kind {
	case ChatMessage:
		m, _ := in["message"].(string)
		if strings.TrimSpace(m) == "" {
			return nil, errors.New("message: the question, a text")
		}
		if v, ok := in["as_of"]; ok && v != nil && v != "" {
			d, _ := v.(string)
			if _, err := time.Parse("2006-01-02", d); err != nil {
				return nil, errors.New("as_of: a day, YYYY-MM-DD")
			}
		}
	case ChatHandoff:
		if err := optionalString(in, "message", maxChatNote); err != nil {
			return nil, err
		}
		if err := optionalString(in, "contact", maxChatContact); err != nil {
			return nil, err
		}
	case ChatRating:
		turn, _ := in["turn"].(string)
		if strings.TrimSpace(turn) == "" || len(turn) > maxChatID {
			return nil, errors.New("turn: the answer rated, the id of its run")
		}
		r, ok := asFloat(in["rating"])
		if !ok || r != math.Trunc(r) || r < -10 || r > 10 {
			return nil, errors.New("rating: a whole number from -10 to 10")
		}
		if err := optionalString(in, "comment", 0); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("kind %q: message, rating or handoff", kind)
	}
	if v, ok := in["history"]; ok && v != nil {
		h, ok := v.([]any)
		if !ok || len(h) > maxChatHistory {
			return nil, fmt.Errorf("history: a list of at most %d turns, {role, content}", maxChatHistory)
		}
		for i, t := range h {
			turn, _ := t.(map[string]any)
			role, _ := turn["role"].(string)
			_, text := turn["content"].(string)
			if (role != "user" && role != "assistant") || !text {
				return nil, fmt.Errorf("history[%d]: {role: user or assistant, content: a text}", i)
			}
		}
	}
	if v, ok := in["scopes"]; ok && v != nil {
		sc, ok := v.([]any)
		if !ok || len(sc) > maxChatScopes {
			return nil, fmt.Errorf("scopes: a list of at most %d names", maxChatScopes)
		}
		for i, x := range sc {
			if name, ok := x.(string); !ok || name == "" || len(name) > maxChatID {
				return nil, fmt.Errorf("scopes[%d]: the name of a scope", i)
			}
		}
	}
	return out, nil
}

// asFloat is a number of a decoded JSON value, or of a Go caller's.
func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// optionalString checks that in[key], when set, is a string of at most
// max bytes (0: any).
func optionalString(in map[string]any, key string, max int) error {
	v, ok := in[key]
	if !ok || v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok || (max > 0 && len(s) > max) {
		if max > 0 {
			return fmt.Errorf("%s: a text of at most %d bytes", key, max)
		}
		return fmt.Errorf("%s: a text", key)
	}
	return nil
}
