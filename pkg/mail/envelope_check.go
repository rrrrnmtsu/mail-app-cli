package mail

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IndexCheckMailbox is one mailbox compared between the index and AppleScript.
type IndexCheckMailbox struct {
	Account    string
	Mailbox    string
	Compared   int
	Mismatches []string
	Skipped    string
}

// IndexCheckResult reports whether the index can stand in for AppleScript.
type IndexCheckResult struct {
	Path      string
	Build     string
	Mailboxes []IndexCheckMailbox
	OK        bool
	Marker    string
}

// CheckIndex lists each enabled account's INBOX and sent mailbox through both
// AppleScript and the index and compares them field by field. Only a clean
// run records the marker that turns the index path on.
func (c *Client) CheckIndex(limit int) (*IndexCheckResult, error) {
	path, err := findEnvelopeIndex()
	if err != nil {
		return nil, err
	}
	res := &IndexCheckResult{Path: path, Build: osBuild()}
	e := &envelopeIndex{path: path}
	all, err := e.mailboxes()
	if err != nil {
		return nil, fmt.Errorf("index schema not as expected: %w", err)
	}
	accounts, err := c.GetAccountsJSON()
	if err != nil {
		return nil, err
	}
	jxa := &Client{noIndex: true}
	for _, acc := range accounts {
		if !acc.Enabled {
			continue
		}
		names := []string{"INBOX"}
		for _, n := range specialMailboxCandidates["sent"] {
			if _, ok := resolveIndexMailbox(all, acc.ID, n); ok {
				names = append(names, n)
				break
			}
		}
		for _, name := range names {
			mb := IndexCheckMailbox{Account: acc.Name, Mailbox: name}
			target, ok := resolveIndexMailbox(all, acc.ID, name)
			if !ok {
				mb.Mismatches = append(mb.Mismatches, "mailbox not found in index (account id "+acc.ID+")")
				res.Mailboxes = append(res.Mailboxes, mb)
				continue
			}
			want, err := jxa.GetMessagesJSON(acc.Name, name, limit, 0, false, false, false, "")
			if err != nil {
				mb.Skipped = "AppleScript failed: " + err.Error()
				res.Mailboxes = append(res.Mailboxes, mb)
				continue
			}
			got, err := e.listMessages([]indexTarget{{RowID: target.RowID, AccountName: acc.Name, MailboxName: name}}, indexQuery{Limit: limit})
			if err != nil {
				return nil, fmt.Errorf("index query failed: %w", err)
			}
			mb.Compared = len(want)
			mb.Mismatches = compareListings(want, got, limit)
			res.Mailboxes = append(res.Mailboxes, mb)
		}
	}

	res.OK = len(res.Mailboxes) > 0
	compared := 0
	for _, mb := range res.Mailboxes {
		compared += mb.Compared
		if len(mb.Mismatches) > 0 {
			res.OK = false
		}
	}
	if compared == 0 {
		res.OK = false
	}
	if res.OK {
		marker, err := envelopeMarkerPath()
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(marker, []byte(markerValue(path)), 0o644); err != nil {
			return nil, err
		}
		res.Marker = marker
	}
	return res, nil
}

// compareListings checks every AppleScript row against the index. The
// AppleScript path drops deleted rows after taking `limit`, so the index may
// legitimately return up to that many extra (older) rows.
func compareListings(want, got []Message, limit int) []string {
	var out []string
	byID := map[string]Message{}
	for _, m := range got {
		byID[m.ID] = m
	}
	wantIDs := map[string]bool{}
	for _, w := range want {
		wantIDs[w.ID] = true
		g, ok := byID[w.ID]
		if !ok {
			out = append(out, fmt.Sprintf("id %s (%q) missing from index", w.ID, w.Subject))
			continue
		}
		for _, d := range []struct{ field, a, b string }{
			{"subject", w.Subject, g.Subject},
			{"sender", normalizeSender(w.Sender), normalizeSender(g.Sender)},
			{"dateReceived", w.DateReceived, g.DateReceived},
			{"dateSent", w.DateSent, g.DateSent},
			{"read", fmt.Sprint(w.Read), fmt.Sprint(g.Read)},
			{"flagged", fmt.Sprint(w.Flagged), fmt.Sprint(g.Flagged)},
			{"flagIndex", fmt.Sprint(w.FlagIndex), fmt.Sprint(g.FlagIndex)},
		} {
			if d.a != d.b {
				out = append(out, fmt.Sprintf("id %s %s: applescript=%q index=%q", w.ID, d.field, d.a, d.b))
			}
		}
	}
	extra := 0
	for _, g := range got {
		if !wantIDs[g.ID] {
			extra++
		}
	}
	if allowed := limit - len(want); extra > allowed {
		out = append(out, fmt.Sprintf("index returned %d rows AppleScript did not (allowed %d)", extra, allowed))
	}
	return out
}

// normalizeSender ignores quoting differences around display names.
func normalizeSender(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, `"`, "")), " ")
}

// IndexStatus explains whether listings currently use the index.
func IndexStatus() string {
	if _, err := openVerifiedIndex(); err != nil {
		return "AppleScript (index off: " + err.Error() + ")"
	}
	return "Envelope Index (verified)"
}
