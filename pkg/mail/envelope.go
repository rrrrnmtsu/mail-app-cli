package mail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
)

// Mail.app keeps its message list in a SQLite database ("Envelope Index").
// Reading it answers a mailbox listing in milliseconds where AppleScript
// takes seconds, but the schema is private and the file needs Full Disk
// Access. So the index is only used after `index check` has compared it with
// AppleScript on this machine and OS build and recorded a marker; anything
// unexpected (no access, schema drift, unknown mailbox) falls back to
// AppleScript.

const (
	envelopeMarkerName = "envelope-index-verified"
	sqliteBin          = "/usr/bin/sqlite3"
	// DisableIndexEnv turns the index path off without deleting the marker.
	DisableIndexEnv = "MAIL_APP_CLI_NO_INDEX"
)

type envelopeIndex struct {
	path string
}

// findEnvelopeIndex returns the newest Mail data version's index path.
func findEnvelopeIndex() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dirs, err := filepath.Glob(filepath.Join(home, "Library", "Mail", "V*"))
	if err != nil || len(dirs) == 0 {
		return "", fmt.Errorf("no Mail data directory found under ~/Library/Mail (Full Disk Access is required to see it)")
	}
	sort.Slice(dirs, func(i, j int) bool { return versionOf(dirs[i]) > versionOf(dirs[j]) })
	p := filepath.Join(dirs[0], "MailData", "Envelope Index")
	f, err := os.Open(p)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", p, err)
	}
	f.Close()
	return p, nil
}

func versionOf(dir string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(filepath.Base(dir), "V"))
	return n
}

func osBuild() string {
	out, err := exec.Command("sw_vers", "-buildVersion").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func envelopeMarkerPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "mail-app-cli", envelopeMarkerName), nil
}

// markerValue ties a verification to the index file and OS build it was made
// on; an OS update can change the private schema.
func markerValue(indexPath string) string {
	return indexPath + "\n" + osBuild() + "\n"
}

// openVerifiedIndex returns the index only when it is readable and a check
// on this OS build passed. Errors are expected and mean "use AppleScript".
func openVerifiedIndex() (*envelopeIndex, error) {
	if os.Getenv(DisableIndexEnv) != "" {
		return nil, fmt.Errorf("%s is set", DisableIndexEnv)
	}
	marker, err := envelopeMarkerPath()
	if err != nil {
		return nil, err
	}
	want, err := os.ReadFile(marker)
	if err != nil {
		return nil, fmt.Errorf("index not verified (run `mail-app-cli index check`)")
	}
	p, err := findEnvelopeIndex()
	if err != nil {
		return nil, err
	}
	if string(want) != markerValue(p) {
		return nil, fmt.Errorf("index verified on a different OS build or data version (run `mail-app-cli index check`)")
	}
	return &envelopeIndex{path: p}, nil
}

// IndexEnabled reports whether listings will be answered from the index, so
// callers can skip their own caches.
func IndexEnabled() bool {
	_, err := openVerifiedIndex()
	return err == nil
}

func (e *envelopeIndex) query(sql string, out any) error {
	cmd := exec.Command(sqliteBin, "-readonly", "-json", e.path, sql)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sqlite3: %v - %s", err, strings.TrimSpace(stderr.String()))
	}
	// sqlite3 prints nothing at all for an empty result.
	if len(bytes.TrimSpace(stdout.Bytes())) == 0 {
		return json.Unmarshal([]byte("[]"), out)
	}
	return json.Unmarshal(stdout.Bytes(), out)
}

type indexMailbox struct {
	RowID     int64
	AccountID string
	Name      string
}

// mailboxes lists every mailbox row with the account id and leaf name parsed
// from its URL (e.g. imap://<account-id>/%5BGmail%5D/All%20Mail).
func (e *envelopeIndex) mailboxes() ([]indexMailbox, error) {
	var rows []struct {
		RowID int64  `json:"ROWID"`
		URL   string `json:"url"`
	}
	if err := e.query("SELECT ROWID, url FROM mailboxes;", &rows); err != nil {
		return nil, err
	}
	out := make([]indexMailbox, 0, len(rows))
	for _, r := range rows {
		u, err := url.Parse(r.URL)
		if err != nil || u.Host == "" {
			continue
		}
		path := strings.Trim(u.Path, "/")
		if path == "" {
			continue
		}
		segs := strings.Split(path, "/")
		// URLs store names decomposed (ゴ as コ + ゛); AppleScript returns
		// them composed.
		name := norm.NFC.String(segs[len(segs)-1])
		out = append(out, indexMailbox{RowID: r.RowID, AccountID: u.Host, Name: name})
	}
	return out, nil
}

// resolveMailbox finds the single mailbox of an account with the given leaf
// name; ambiguity is reported as not found so the caller falls back.
func resolveIndexMailbox(all []indexMailbox, accountID, name string) (indexMailbox, bool) {
	var hit []indexMailbox
	for _, m := range all {
		if strings.EqualFold(m.AccountID, accountID) && m.Name == norm.NFC.String(name) {
			hit = append(hit, m)
		}
	}
	if len(hit) != 1 {
		return indexMailbox{}, false
	}
	return hit[0], true
}

type indexTarget struct {
	RowID       int64
	AccountName string
	MailboxName string
}

type indexQuery struct {
	Limit, Offset int
	UnreadOnly    bool
	FlaggedOnly   bool
	Since         string
}

func (e *envelopeIndex) listMessages(targets []indexTarget, q indexQuery) ([]Message, error) {
	if len(targets) == 0 {
		return []Message{}, nil
	}
	byRow := map[int64]indexTarget{}
	ids := make([]string, 0, len(targets))
	for _, t := range targets {
		byRow[t.RowID] = t
		ids = append(ids, strconv.FormatInt(t.RowID, 10))
	}
	in := strings.Join(ids, ",")
	where := []string{"m.deleted = 0"}
	if q.UnreadOnly {
		where = append(where, "m.read = 0")
	}
	if q.FlaggedOnly {
		where = append(where, "m.flagged = 1")
	}
	if q.Since != "" {
		t, err := parseSince(q.Since)
		if err != nil {
			return nil, err
		}
		where = append(where, "m.date_received >= "+strconv.FormatInt(t.Unix(), 10))
	}
	limit := ""
	if q.Limit > 0 {
		limit = fmt.Sprintf(" LIMIT %d OFFSET %d", q.Limit, q.Offset)
	} else if q.Offset > 0 {
		limit = fmt.Sprintf(" LIMIT -1 OFFSET %d", q.Offset)
	}
	// Gmail stores each message once, in All Mail; INBOX, Sent and user
	// labels are rows in labels pointing at it. Other IMAP accounts keep
	// messages.mailbox. The mailbox reported is the one asked for.
	sql := `WITH hits(mid, box) AS (
	SELECT ROWID, mailbox FROM messages WHERE mailbox IN (` + in + `)
	UNION SELECT message_id, mailbox_id FROM labels WHERE mailbox_id IN (` + in + `)
)
SELECT m.ROWID AS id,
	COALESCE(m.subject_prefix, '') || COALESCE(s.subject, '') AS subject,
	COALESCE(a.address, '') AS address, COALESCE(a.comment, '') AS comment,
	m.date_received AS received, m.date_sent AS sent,
	m.read AS read, m.flagged AS flagged, m.flags AS flags, h.box AS mailbox
FROM hits h
JOIN messages m ON m.ROWID = h.mid
LEFT JOIN subjects s ON s.ROWID = m.subject
LEFT JOIN addresses a ON a.ROWID = m.sender
WHERE ` + strings.Join(where, " AND ") + `
ORDER BY m.date_received DESC, m.ROWID DESC` + limit + ";"

	var rows []struct {
		ID       int64  `json:"id"`
		Subject  string `json:"subject"`
		Address  string `json:"address"`
		Comment  string `json:"comment"`
		Received int64  `json:"received"`
		Sent     int64  `json:"sent"`
		Read     int    `json:"read"`
		Flagged  int    `json:"flagged"`
		Flags    int64  `json:"flags"`
		Mailbox  int64  `json:"mailbox"`
	}
	if err := e.query(sql, &rows); err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(rows))
	for _, r := range rows {
		t := byRow[r.Mailbox]
		flagIndex := -1
		if r.Flagged != 0 {
			// The flag_color column is just 1 whenever flagged; the colour
			// itself sits in bits 39-41 of flags (matched against
			// AppleScript's flag index by `index check`).
			flagIndex = int((r.Flags >> 39) & 7)
		}
		out = append(out, Message{
			ID:           strconv.FormatInt(r.ID, 10),
			Subject:      r.Subject,
			Sender:       formatSender(r.Comment, r.Address),
			DateReceived: isoUTC(r.Received),
			DateSent:     isoUTC(r.Sent),
			Read:         r.Read != 0,
			Flagged:      r.Flagged != 0,
			FlagIndex:    flagIndex,
			Mailbox:      t.MailboxName,
			Account:      t.AccountName,
		})
	}
	return out, nil
}

// formatSender mirrors AppleScript's `sender`: "Name <addr>" or bare addr.
func formatSender(name, addr string) string {
	if name == "" {
		return addr
	}
	return name + " <" + addr + ">"
}

// isoUTC matches JavaScript's Date.toISOString(), which the AppleScript path
// emits, so both paths sort and compare identically.
func isoUTC(unix int64) string {
	return time.Unix(unix, 0).UTC().Format("2006-01-02T15:04:05.000Z")
}

// parseSince follows JavaScript's Date parsing used by the AppleScript path:
// a bare date is UTC midnight, a date with time is local time.
func parseSince(s string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid --since %q (want YYYY-MM-DD or YYYY-MM-DD HH:MM:SS)", s)
}

// accountTargets resolves (account name, candidate mailbox names) pairs to
// index rows. ok is false when any account can't be mapped, since a silently
// missing account would look like an empty mailbox.
func (e *envelopeIndex) accountTargets(accounts []Account, candidates []string, skipMissing bool) ([]indexTarget, bool, error) {
	all, err := e.mailboxes()
	if err != nil {
		return nil, false, err
	}
	var targets []indexTarget
	for _, acc := range accounts {
		found := false
		for _, name := range candidates {
			if m, ok := resolveIndexMailbox(all, acc.ID, name); ok {
				targets = append(targets, indexTarget{RowID: m.RowID, AccountName: acc.Name, MailboxName: name})
				found = true
				break
			}
		}
		if !found && !skipMissing {
			return nil, false, nil
		}
	}
	return targets, true, nil
}

// indexListMessages answers GetMessagesJSON from the index. handled is false
// when the caller should fall back to AppleScript.
func (c *Client) indexListMessages(accountName, mailboxName string, q indexQuery) ([]Message, bool) {
	if c.noIndex {
		return nil, false
	}
	e, err := openVerifiedIndex()
	if err != nil {
		return nil, false
	}
	accounts, err := c.cachedAccounts()
	if err != nil {
		return nil, false
	}
	var acc *Account
	for i := range accounts {
		if accounts[i].Name == accountName {
			acc = &accounts[i]
		}
	}
	if acc == nil {
		return nil, false
	}
	targets, ok, err := e.accountTargets([]Account{*acc}, []string{mailboxName}, false)
	if err != nil || !ok {
		return nil, false
	}
	msgs, err := e.listMessages(targets, q)
	if err != nil {
		return nil, false
	}
	return msgs, true
}

// indexUnified answers a unified view from the index.
func (c *Client) indexUnified(mailboxType string, limit, offset int) ([]Message, bool) {
	if c.noIndex {
		return nil, false
	}
	e, err := openVerifiedIndex()
	if err != nil {
		return nil, false
	}
	accounts, err := c.cachedAccounts()
	if err != nil {
		return nil, false
	}
	return e.unified(accounts, mailboxType, limit, offset)
}

func (e *envelopeIndex) unified(accounts []Account, mailboxType string, limit, offset int) ([]Message, bool) {
	q := indexQuery{Limit: limit, Offset: offset}
	var candidates []string
	var use []Account
	switch mailboxType {
	case "inbox", "unread", "flagged":
		candidates = []string{"INBOX"}
		q.UnreadOnly = mailboxType == "unread"
		q.FlaggedOnly = mailboxType == "flagged"
		for _, a := range accounts {
			if a.Enabled {
				use = append(use, a)
			}
		}
	default:
		cands, ok := specialMailboxCandidates[mailboxType]
		if !ok {
			return nil, false
		}
		candidates = cands
		use = accounts
	}
	// Accounts without e.g. a Junk mailbox are skipped by the AppleScript
	// path too; only INBOX must exist everywhere.
	targets, ok, err := e.accountTargets(use, candidates, mailboxType != "inbox" && mailboxType != "unread" && mailboxType != "flagged")
	if err != nil || !ok {
		return nil, false
	}
	msgs, err := e.listMessages(targets, q)
	if err != nil {
		return nil, false
	}
	return msgs, true
}

// cachedAccounts avoids a ~0.3s AppleScript round trip on every listing;
// account ids only change when accounts are added or removed.
func (c *Client) cachedAccounts() ([]Account, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	p := filepath.Join(home, ".cache", "mail-app-cli", "index-accounts.json")
	if info, err := os.Stat(p); err == nil && time.Since(info.ModTime()) < time.Hour {
		if b, err := os.ReadFile(p); err == nil {
			var accs []Account
			if json.Unmarshal(b, &accs) == nil && len(accs) > 0 {
				return accs, nil
			}
		}
	}
	accs, err := c.GetAccountsJSON()
	if err != nil {
		return nil, err
	}
	if b, err := json.Marshal(accs); err == nil {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, b, 0o644)
	}
	return accs, nil
}
