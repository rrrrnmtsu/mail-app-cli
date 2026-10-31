package mail

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// buildIndex creates a miniature Envelope Index with the columns the index
// path reads, so the SQL and row mapping are exercised without Mail.app.
func buildIndex(t *testing.T) *envelopeIndex {
	t.Helper()
	p := filepath.Join(t.TempDir(), "Envelope Index")
	schema := `
CREATE TABLE mailboxes (ROWID INTEGER PRIMARY KEY, url TEXT);
CREATE TABLE subjects (ROWID INTEGER PRIMARY KEY, subject TEXT);
CREATE TABLE addresses (ROWID INTEGER PRIMARY KEY, address TEXT, comment TEXT);
CREATE TABLE labels (message_id INTEGER, mailbox_id INTEGER);
CREATE TABLE messages (ROWID INTEGER PRIMARY KEY, subject_prefix TEXT, subject INTEGER, sender INTEGER,
  date_received INTEGER, date_sent INTEGER, read INTEGER, flagged INTEGER, flags INTEGER, mailbox INTEGER, deleted INTEGER);
INSERT INTO mailboxes VALUES (1, 'imap://AAAA-1111/INBOX'), (2, 'imap://AAAA-1111/%5BGmail%5D/Sent%20Mail'),
  (3, 'imap://BBBB-2222/INBOX'), (4, 'imap://BBBB-2222/Archive/INBOX'),
  (5, 'imap://CCCC-3333/%5BGmail%5D/All%20Mail'), (6, 'imap://CCCC-3333/INBOX'),
  (7, 'imap://CCCC-3333/%5BGmail%5D/%E3%82%B3%E3%82%99%E3%83%9F%E7%AE%B1');
INSERT INTO subjects VALUES (1, 'hello'), (2, 'report');
INSERT INTO addresses VALUES (1, 'a@example.com', 'Alice'), (2, 'b@example.com', '');
INSERT INTO messages VALUES
  (10, '', 1, 1, 1700000300, 1700000200, 0, 0, 0, 1, 0),
  (11, 'Re: ', 2, 2, 1700000100, 1700000000, 1, 1, 549755813888, 1, 0),
  (12, '', 1, 1, 1700000400, 1700000400, 0, 0, 0, 1, 1),
  (13, '', 2, 2, 1700000500, 1700000500, 1, 0, 0, 2, 0),
  (20, '', 1, 1, 1700000600, 1700000600, 0, 0, 0, 5, 0),
  (21, '', 2, 2, 1700000700, 1700000700, 0, 0, 0, 5, 0);
INSERT INTO labels VALUES (20, 6);
`
	if out, err := exec.Command(sqliteBin, p, schema).CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v %s", err, out)
	}
	return &envelopeIndex{path: p}
}

func TestIndexMailboxesParseAccountAndLeafName(t *testing.T) {
	all, err := buildIndex(t).mailboxes()
	if err != nil {
		t.Fatal(err)
	}
	m, ok := resolveIndexMailbox(all, "AAAA-1111", "Sent Mail")
	if !ok || m.RowID != 2 {
		t.Fatalf("Sent Mail under [Gmail] should resolve to row 2, got %+v ok=%v", m, ok)
	}
}

func TestIndexMailboxAmbiguousLeafNameFallsBack(t *testing.T) {
	all, _ := buildIndex(t).mailboxes()
	if _, ok := resolveIndexMailbox(all, "BBBB-2222", "INBOX"); ok {
		t.Fatal("two INBOX leaves in one account must not resolve")
	}
}

func TestIndexListSkipsDeletedAndSortsNewestFirst(t *testing.T) {
	e := buildIndex(t)
	msgs, err := e.listMessages([]indexTarget{{RowID: 1, AccountName: "Work", MailboxName: "INBOX"}}, indexQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].ID != "10" || msgs[1].ID != "11" {
		t.Fatalf("want ids [10 11], got %+v", msgs)
	}
	m := msgs[1]
	if m.Subject != "Re: report" || m.Sender != "b@example.com" || !m.Read || !m.Flagged || m.FlagIndex != 1 {
		t.Fatalf("row 11 mapped wrong: %+v", m)
	}
	if msgs[0].Sender != "Alice <a@example.com>" || msgs[0].FlagIndex != -1 || msgs[0].Account != "Work" {
		t.Fatalf("row 10 mapped wrong: %+v", msgs[0])
	}
	if msgs[0].DateReceived != "2023-11-14T22:18:20.000Z" {
		t.Fatalf("date must match JS toISOString, got %s", msgs[0].DateReceived)
	}
}

func TestIndexListFilters(t *testing.T) {
	e := buildIndex(t)
	tg := []indexTarget{{RowID: 1}, {RowID: 2}}
	unread, _ := e.listMessages(tg, indexQuery{UnreadOnly: true})
	flagged, _ := e.listMessages(tg, indexQuery{FlaggedOnly: true})
	paged, _ := e.listMessages(tg, indexQuery{Limit: 1, Offset: 1})
	if len(unread) != 1 || unread[0].ID != "10" {
		t.Fatalf("unread: %+v", unread)
	}
	if len(flagged) != 1 || flagged[0].ID != "11" {
		t.Fatalf("flagged: %+v", flagged)
	}
	if len(paged) != 1 || paged[0].ID != "10" {
		t.Fatalf("offset 1 limit 1 across mailboxes should be row 10: %+v", paged)
	}
}

func TestIndexEmptyResultIsEmptyNotError(t *testing.T) {
	msgs, err := buildIndex(t).listMessages([]indexTarget{{RowID: 3}}, indexQuery{Limit: 5})
	if err != nil || len(msgs) != 0 {
		t.Fatalf("want empty, got %v %v", msgs, err)
	}
}

func TestParseSinceMatchesJavaScriptDateParsing(t *testing.T) {
	d, _ := parseSince("2026-10-01")
	if !d.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("bare date is UTC midnight in JS, got %v", d)
	}
	dt, _ := parseSince("2026-10-01 09:00:00")
	if !dt.Equal(time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)) {
		t.Fatalf("date-time is local in JS, got %v", dt)
	}
}

func TestCompareListingsAllowsDeletedShortfallOnly(t *testing.T) {
	want := []Message{{ID: "1", Subject: "a", FlagIndex: -1}}
	got := []Message{{ID: "1", Subject: "a", FlagIndex: -1}, {ID: "2"}}
	if m := compareListings(want, got, 2); len(m) != 0 {
		t.Fatalf("one extra row within limit is fine: %v", m)
	}
	if m := compareListings(want, got, 1); len(m) == 0 {
		t.Fatal("extra row beyond limit must be reported")
	}
	got[0].Subject = "b"
	if m := compareListings(want, got, 2); len(m) != 1 {
		t.Fatalf("subject mismatch must be reported once: %v", m)
	}
}

func TestOpenVerifiedIndexOffWithoutMarker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if IndexEnabled() {
		t.Fatal("index must stay off until a check has passed")
	}
}

func TestGmailInboxIsReadThroughLabels(t *testing.T) {
	e := buildIndex(t)
	all, _ := e.mailboxes()
	inbox, ok := resolveIndexMailbox(all, "CCCC-3333", "INBOX")
	if !ok {
		t.Fatal("Gmail INBOX should resolve")
	}
	msgs, err := e.listMessages([]indexTarget{{RowID: inbox.RowID, AccountName: "G", MailboxName: "INBOX"}}, indexQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].ID != "20" || msgs[0].Mailbox != "INBOX" {
		t.Fatalf("only the labelled All Mail message belongs to INBOX: %+v", msgs)
	}
}

func TestDecomposedJapaneseMailboxNameMatchesComposed(t *testing.T) {
	all, _ := buildIndex(t).mailboxes()
	if _, ok := resolveIndexMailbox(all, "CCCC-3333", "ゴミ箱"); !ok {
		t.Fatal("ゴミ箱 stored as コ+゛ in the URL must still resolve")
	}
}

func TestSenderComparisonIgnoresSpaceBeforeAddress(t *testing.T) {
	if normalizeSender("送信専用<a@b.jp>") != normalizeSender("送信専用 <a@b.jp>") {
		t.Fatal("raw header without the space is the same sender")
	}
	if normalizeSender("A <a@b.jp>") == normalizeSender("B <a@b.jp>") {
		t.Fatal("different display names must still differ")
	}
}
