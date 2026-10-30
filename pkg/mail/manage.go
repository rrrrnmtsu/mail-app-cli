package mail

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LocalAccountName is the name under which the local "On My Mac" mailbox
// store is addressed and reported. It is not an element of Mail's
// `accounts`; its mailboxes are the application's own top-level mailboxes.
const LocalAccountName = "On My Mac"

// localAccountAliases are the (lower-cased) names accepted for the local
// store: the English and Japanese sidebar labels plus a short alias.
var localAccountAliases = []string{"on my mac", "このmac内", "local"}

// IsLocalAccount reports whether name addresses the local "On My Mac" store.
func IsLocalAccount(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, a := range localAccountAliases {
		if n == a {
			return true
		}
	}
	return false
}

func jsLocalAccountAliases() string {
	quoted := make([]string, len(localAccountAliases))
	for i, a := range localAccountAliases {
		quoted[i] = jsQuote(a)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// jsQuote returns s as a single-quoted, escaped JavaScript string literal.
func jsQuote(s string) string {
	return "'" + escapeJSString(s) + "'"
}

// asQuote returns s as a double-quoted, escaped AppleScript string literal.
func asQuote(s string) string {
	return `"` + escapeAppleScriptString(s) + `"`
}

// jsMain wraps a JXA function body so that whatever it returns, or any
// exception it throws, comes back as a single JSON document. Errors are
// reported as {"error": "..."} instead of the legacy "Error: ..." text,
// because a substring check for "Error" misfires on mailbox or rule names
// that happen to contain that word.
func jsMain(body string) string {
	return "\nconst mail = Application('Mail');\n" + jsResolveMailbox + jsMailboxPathHelpers + `
function main() {
` + body + `
}
let __result;
try { __result = main(); } catch (e) { __result = { error: String(e) }; }
JSON.stringify(__result === undefined ? {} : __result);
`
}

// runJXAJSON runs a jsMain-wrapped script and decodes its result into out
// (which may be nil), turning an {"error": ...} result into a Go error.
func (c *Client) runJXAJSON(script string, out interface{}) error {
	output, err := c.runJXA(script)
	if err != nil {
		return err
	}
	var envelope struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		return fmt.Errorf("unexpected JXA output: %q", output)
	}
	if envelope.Error != "" {
		return errors.New(envelope.Error)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(output), out); err != nil {
			return fmt.Errorf("failed to parse JXA result: %w", err)
		}
	}
	return nil
}

// jsMailboxPathHelpers resolves mailboxes by slash-separated path
// ("Parent/Child") as well as by bare leaf name, and reports the full path
// of what was found. Leaf-name lookup alone is ambiguous (Gmail accounts
// commonly have the same label name at several levels), so commands that
// change the mailbox tree accept a path.
const jsMailboxPathHelpers = `
function resolveMailboxPath(acc, path) {
	const segs = String(path).split('/');
	let level = acc.mailboxes();
	let found = null;
	for (let s = 0; s < segs.length; s++) {
		found = null;
		for (let j = 0; j < level.length; j++) {
			if (level[j].name() === segs[s]) { found = level[j]; break; }
		}
		if (!found) return null;
		try { level = found.mailboxes(); } catch (e) { level = []; }
	}
	return found;
}
function resolveMailboxWithPath(acc, spec) {
	if (String(spec).indexOf('/') >= 0) {
		const m = resolveMailboxPath(acc, spec);
		return m ? { mbox: m, path: String(spec) } : null;
	}
	function walk(mailboxes, prefix) {
		for (let j = 0; j < mailboxes.length; j++) {
			const n = mailboxes[j].name();
			if (n === spec) return { mbox: mailboxes[j], path: prefix + n };
			try {
				const sub = mailboxes[j].mailboxes();
				if (sub.length > 0) {
					const r = walk(sub, prefix + n + '/');
					if (r) return r;
				}
			} catch (e) {}
		}
		return null;
	}
	return walk(acc.mailboxes(), '');
}
`

// ---------------------------------------------------------------------------
// Flag colours
// ---------------------------------------------------------------------------

// FlagColorNames are Mail's flag colours indexed by `flag index`.
var FlagColorNames = []string{"red", "orange", "yellow", "green", "blue", "purple", "gray"}

// ValidateFlagColor checks that n is one of Mail's flag indices (0-6).
func ValidateFlagColor(n int) error {
	if n < 0 || n >= len(FlagColorNames) {
		return fmt.Errorf("invalid flag color %d: must be 0-%d (%s)", n, len(FlagColorNames)-1, strings.Join(colorLegend(), ", "))
	}
	return nil
}

func colorLegend() []string {
	out := make([]string, len(FlagColorNames))
	for i, n := range FlagColorNames {
		out[i] = fmt.Sprintf("%d=%s", i, n)
	}
	return out
}

func buildSetFlagColorScript(accountName, mailboxName, messageID string, color int) string {
	return jsMain(fmt.Sprintf(`
	const acc = getAccount(%s);
	const mbox = resolveMailbox(acc, %s);
	if (!mbox) throw 'Mailbox not found';
	const idx = mbox.messages.id().findIndex(id => String(id) === %s);
	if (idx < 0) throw 'Message not found';
	const msg = mbox.messages.at(idx);
	msg.flaggedStatus = true;
	msg.flagIndex = %d;
	return { flagged: msg.flaggedStatus(), flagIndex: msg.flagIndex() };
`, jsQuote(accountName), jsQuote(mailboxName), jsQuote(messageID), color))
}

// SetFlagColor flags a message with the given colour (0-6) and reads the
// flag back so a silently ignored write is reported instead of claimed.
func (c *Client) SetFlagColor(accountName, mailboxName, messageID string, color int) error {
	if err := ValidateFlagColor(color); err != nil {
		return err
	}
	var res struct {
		Flagged   bool `json:"flagged"`
		FlagIndex int  `json:"flagIndex"`
	}
	if err := c.runJXAJSON(buildSetFlagColorScript(accountName, mailboxName, messageID, color), &res); err != nil {
		return err
	}
	if !res.Flagged || res.FlagIndex != color {
		return fmt.Errorf("Mail.app did not apply the flag color (now flagged=%v, flagIndex=%d)", res.Flagged, res.FlagIndex)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Mailbox management
// ---------------------------------------------------------------------------

// ValidateMailboxName rejects names that Mail would interpret as a path or
// that cannot be expressed safely in a script.
func ValidateMailboxName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("mailbox name must not be empty")
	}
	if strings.Contains(name, "/") {
		return fmt.Errorf("mailbox name %q must not contain '/' (use --parent to nest)", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("mailbox name %q contains control characters", name)
		}
	}
	return nil
}

// protectedMailboxNames are system/special mailboxes that `mailboxes
// delete` and `mailboxes rename` must never touch, whatever their content.
var protectedMailboxNames = func() map[string]bool {
	m := map[string]bool{
		"INBOX": true, "Inbox": true, "Outbox": true, "SendLater": true,
		"All Mail": true, "すべてのメール": true, "Starred": true, "スター付き": true,
		"Important": true, "重要": true, "[Gmail]": true,
	}
	for _, names := range specialMailboxCandidates {
		for _, n := range names {
			m[n] = true
		}
	}
	return m
}()

// IsProtectedMailbox reports whether the mailbox (given as name or path)
// is a system mailbox that must not be renamed or deleted.
func IsProtectedMailbox(nameOrPath string) bool {
	leaf := nameOrPath
	if i := strings.LastIndex(nameOrPath, "/"); i >= 0 {
		leaf = nameOrPath[i+1:]
	}
	return protectedMailboxNames[leaf] || protectedMailboxNames[nameOrPath]
}

// MailboxDeleteDecision decides whether a mailbox may be deleted. Deleting
// a mailbox in Mail deletes the messages inside it (and its children), so a
// non-empty mailbox requires an explicit force.
func MailboxDeleteDecision(path string, messages, children int, force bool) error {
	if IsProtectedMailbox(path) {
		return fmt.Errorf("refusing to delete system mailbox %q", path)
	}
	if force {
		return nil
	}
	if messages > 0 || children > 0 {
		return fmt.Errorf("mailbox %q is not empty (%d message(s), %d child mailbox(es)); deleting it would delete them too — move them out first or pass --force", path, messages, children)
	}
	return nil
}

// MailboxInfo describes a mailbox resolved by path.
type MailboxInfo struct {
	Account  string
	Path     string
	Messages int
	Children int
}

func buildMailboxCreatePrepareScript(accountName, name, parent string) string {
	return jsMain(fmt.Sprintf(`
	const acc = getAccount(%s);
	const name = %s;
	const parent = %s;
	let path = name;
	if (parent !== '') {
		const p = resolveMailboxWithPath(acc, parent);
		if (!p) throw 'Parent mailbox not found: ' + parent;
		path = p.path + '/' + name;
	}
	if (resolveMailboxPath(acc, path)) throw 'Mailbox already exists: ' + path;
	return { account: acc.name(), path: path, local: isLocalAccount(acc) };
`, jsQuote(accountName), jsQuote(name), jsQuote(parent)))
}

// buildMailboxCreateAppleScript uses `make new mailbox`. Mail interprets a
// slash-separated name as a path and creates the mailbox under that parent.
// The local store is addressed by making the mailbox at application level;
// an account by making it inside a `tell account` block, where Cocoa
// scripting uses the tell target as the insertion container.
func buildMailboxCreateAppleScript(accountName, path string, local bool) string {
	if local {
		return fmt.Sprintf(`tell application "Mail"
	make new mailbox with properties {name:%s}
	return "Success"
end tell`, asQuote(path))
	}
	return fmt.Sprintf(`tell application "Mail"
	tell account %s
		make new mailbox with properties {name:%s}
	end tell
	return "Success"
end tell`, asQuote(accountName), asQuote(path))
}

func buildMailboxInspectScript(accountName, spec string) string {
	return jsMain(fmt.Sprintf(`
	const acc = getAccount(%s);
	const r = resolveMailboxWithPath(acc, %s);
	if (!r) return { found: false, account: acc.name() };
	let messages = 0, children = 0;
	try { messages = r.mbox.messages.length; } catch (e) {}
	try { children = r.mbox.mailboxes.length; } catch (e) {}
	return { found: true, account: acc.name(), path: r.path, messages: messages, children: children };
`, jsQuote(accountName), jsQuote(spec)))
}

type mailboxInspectResult struct {
	Found    bool   `json:"found"`
	Account  string `json:"account"`
	Path     string `json:"path"`
	Messages int    `json:"messages"`
	Children int    `json:"children"`
}

func (c *Client) inspectMailbox(accountName, spec string) (*mailboxInspectResult, error) {
	var res mailboxInspectResult
	if err := c.runJXAJSON(buildMailboxInspectScript(accountName, spec), &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// CreateMailbox creates NAME (optionally under PARENT, a name or path) in
// the account or the local "On My Mac" store, and confirms it exists
// afterwards.
func (c *Client) CreateMailbox(accountName, name, parent string) (*MailboxInfo, error) {
	if err := ValidateMailboxName(name); err != nil {
		return nil, err
	}
	var prep struct {
		Account string `json:"account"`
		Path    string `json:"path"`
		Local   bool   `json:"local"`
	}
	if err := c.runJXAJSON(buildMailboxCreatePrepareScript(accountName, name, parent), &prep); err != nil {
		return nil, err
	}
	if _, err := c.runAppleScript(buildMailboxCreateAppleScript(accountName, prep.Path, prep.Local)); err != nil {
		return nil, err
	}
	res, err := c.inspectMailbox(accountName, prep.Path)
	if err != nil {
		return nil, fmt.Errorf("created, but could not verify: %w", err)
	}
	if !res.Found {
		return nil, fmt.Errorf("Mail.app accepted the create command but mailbox %q does not exist afterwards", prep.Path)
	}
	return &MailboxInfo{Account: res.Account, Path: res.Path, Messages: res.Messages, Children: res.Children}, nil
}

func buildMailboxRenameScript(accountName, oldSpec, newName string) string {
	return jsMain(fmt.Sprintf(`
	const acc = getAccount(%s);
	const r = resolveMailboxWithPath(acc, %s);
	if (!r) throw 'Mailbox not found: ' + %s;
	const newName = %s;
	const slash = r.path.lastIndexOf('/');
	const parentPath = slash >= 0 ? r.path.substring(0, slash) : '';
	const newPath = parentPath === '' ? newName : parentPath + '/' + newName;
	if (resolveMailboxPath(acc, newPath)) throw 'Mailbox already exists: ' + newPath;
	r.mbox.name = newName;
	const renamed = resolveMailboxPath(acc, newPath);
	if (!renamed) throw 'Mail.app did not apply the rename (mailbox name may be read-only for this account type)';
	return { account: acc.name(), oldPath: r.path, path: newPath };
`, jsQuote(accountName), jsQuote(oldSpec), jsQuote(oldSpec), jsQuote(newName)))
}

// RenameMailbox renames a mailbox in place by setting its `name` property
// (writable per Mail's dictionary), then re-resolves it under the new name.
func (c *Client) RenameMailbox(accountName, oldSpec, newName string) (*MailboxInfo, error) {
	if err := ValidateMailboxName(newName); err != nil {
		return nil, err
	}
	if strings.TrimSpace(oldSpec) == "" {
		return nil, errors.New("--name must not be empty")
	}
	if IsProtectedMailbox(oldSpec) {
		return nil, fmt.Errorf("refusing to rename system mailbox %q", oldSpec)
	}
	var res struct {
		Account string `json:"account"`
		Path    string `json:"path"`
	}
	if err := c.runJXAJSON(buildMailboxRenameScript(accountName, oldSpec, newName), &res); err != nil {
		return nil, err
	}
	return &MailboxInfo{Account: res.Account, Path: res.Path}, nil
}

func buildMailboxDeleteScript(accountName, path string, force bool) string {
	forceJS := "false"
	if force {
		forceJS = "true"
	}
	return jsMain(fmt.Sprintf(`
	const acc = getAccount(%s);
	const path = %s;
	const mbox = resolveMailboxPath(acc, path);
	if (!mbox) throw 'Mailbox not found: ' + path;
	if (!%s) {
		let messages = 0, children = 0;
		try { messages = mbox.messages.length; } catch (e) {}
		try { children = mbox.mailboxes.length; } catch (e) {}
		if (messages > 0 || children > 0) throw 'Mailbox ' + path + ' is no longer empty; not deleting';
	}
	mail.delete(mbox);
	if (resolveMailboxPath(acc, path)) throw 'Mail.app accepted the delete command but mailbox ' + path + ' still exists';
	return { account: acc.name(), path: path };
`, jsQuote(accountName), jsQuote(path), forceJS))
}

// DeleteMailbox deletes a mailbox. Unless force is set it refuses when the
// mailbox holds messages or child mailboxes (the guard is evaluated before
// the delete and re-checked inside the delete script).
func (c *Client) DeleteMailbox(accountName, spec string, force bool) (*MailboxInfo, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, errors.New("--name must not be empty")
	}
	info, err := c.inspectMailbox(accountName, spec)
	if err != nil {
		return nil, err
	}
	if !info.Found {
		return nil, fmt.Errorf("mailbox not found: %s", spec)
	}
	if err := MailboxDeleteDecision(info.Path, info.Messages, info.Children, force); err != nil {
		return nil, err
	}
	var res struct {
		Account string `json:"account"`
		Path    string `json:"path"`
	}
	if err := c.runJXAJSON(buildMailboxDeleteScript(accountName, info.Path, force), &res); err != nil {
		return nil, explainMailboxDeleteError(err)
	}
	return &MailboxInfo{Account: res.Account, Path: res.Path, Messages: info.Messages, Children: info.Children}, nil
}

// explainMailboxDeleteError turns Mail's generic handler failure (-10000)
// into what it actually means. Mail.app's `delete` command rejects
// mailboxes outright — verified on macOS with `delete mailbox "x"`,
// `delete mailbox "c" of mailbox "p"` and `delete (first mailbox whose
// name is "x")`, all -10000 — so the user has to delete it in Mail.app.
func explainMailboxDeleteError(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "-10000") || strings.Contains(msg, "AppleEvent") {
		return fmt.Errorf("Mail.app does not allow deleting mailboxes via scripting; delete it in Mail.app (Mailbox > Delete Mailbox). Underlying error: %w", err)
	}
	return err
}

// ---------------------------------------------------------------------------
// Rule management
// ---------------------------------------------------------------------------

// RuleSpec describes a rule to create. All conditions must match.
type RuleSpec struct {
	Name          string
	From          string
	Subject       string
	To            string
	MoveTo        string // "ACCOUNT/MAILBOX" (mailbox may itself be a path)
	MarkRead      bool
	FlagColor     int // -1 = no flag action
	DeleteMessage bool
}

// ParseMoveTarget splits "ACCOUNT/MAILBOX" at the first slash; the mailbox
// part may itself be a "Parent/Child" path.
func ParseMoveTarget(s string) (account, mailbox string, err error) {
	i := strings.Index(s, "/")
	if i <= 0 || i == len(s)-1 {
		return "", "", fmt.Errorf("--move-to must be \"ACCOUNT/MAILBOX\", got %q", s)
	}
	return s[:i], s[i+1:], nil
}

// Validate enforces at least one condition and one action, and rejects
// contradictory action combinations.
func (r RuleSpec) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return errors.New("--name is required")
	}
	if r.From == "" && r.Subject == "" && r.To == "" {
		return errors.New("at least one condition is required (--from, --subject or --to)")
	}
	hasFlag := r.FlagColor != -1
	if r.MoveTo == "" && !r.MarkRead && !hasFlag && !r.DeleteMessage {
		return errors.New("at least one action is required (--move-to, --mark-read, --flag-color or --delete-message)")
	}
	if hasFlag {
		if err := ValidateFlagColor(r.FlagColor); err != nil {
			return err
		}
	}
	if r.MoveTo != "" {
		if _, _, err := ParseMoveTarget(r.MoveTo); err != nil {
			return err
		}
		if r.DeleteMessage {
			return errors.New("--move-to and --delete-message are mutually exclusive")
		}
	}
	return nil
}

func buildCreateRuleScript(r RuleSpec) string {
	var conds strings.Builder
	add := func(ruleType, expr string) {
		if expr == "" {
			return
		}
		fmt.Fprintf(&conds, "\tconds.push({ ruleType: %s, qualifier: 'does contain value', expression: %s });\n", jsQuote(ruleType), jsQuote(expr))
	}
	add("from header", r.From)
	add("subject header", r.Subject)
	add("to header", r.To)

	moveAcc, moveBox := "", ""
	if r.MoveTo != "" {
		moveAcc, moveBox, _ = ParseMoveTarget(r.MoveTo)
	}
	markRead, del := "false", "false"
	if r.MarkRead {
		markRead = "true"
	}
	if r.DeleteMessage {
		del = "true"
	}

	return jsMain(fmt.Sprintf(`
	const ruleName = %s;
	if (mail.rules.name().indexOf(ruleName) >= 0) throw 'Rule already exists: ' + ruleName;
	const conds = [];
%s
	let dest = null;
	const moveAcc = %s, moveBox = %s;
	if (moveAcc !== '') {
		const r = resolveMailboxWithPath(getAccount(moveAcc), moveBox);
		if (!r) throw 'Move target mailbox not found: ' + moveAcc + '/' + moveBox;
		dest = r.mbox;
	}
	mail.rules.push(mail.Rule({ name: ruleName, enabled: false, allConditionsMustBeMet: true }));
	const idx = mail.rules.name().lastIndexOf(ruleName);
	if (idx < 0) throw 'Mail.app did not create the rule';
	const rule = mail.rules[idx];
	try {
		// A freshly made rule can carry a placeholder condition; drop
		// whatever exists before adding ours so only the requested
		// conditions apply.
		const pre = rule.ruleConditions();
		for (let i = pre.length - 1; i >= 0; i--) mail.delete(pre[i]);
		for (let i = 0; i < conds.length; i++) rule.ruleConditions.push(mail.RuleCondition(conds[i]));
		if (%s) rule.markRead = true;
		if (%d >= 0) { rule.markFlagged = true; rule.markFlagIndex = %d; }
		if (%s) rule.deleteMessage = true;
		if (dest) { rule.moveMessage = dest; rule.shouldMoveMessage = true; }
		rule.enabled = true;
		if (rule.ruleConditions.length !== conds.length) throw 'condition count mismatch after create';
	} catch (e) {
		// Never leave a half-configured (possibly match-everything) rule behind.
		try { mail.delete(rule); } catch (e2) {}
		throw 'Failed to configure rule, rolled back: ' + e;
	}
	return { name: rule.name(), enabled: rule.enabled(), conditions: rule.ruleConditions.length };
`, jsQuote(r.Name), conds.String(), jsQuote(moveAcc), jsQuote(moveBox), markRead, r.FlagColor, r.FlagColor, del))
}

// CreateRule creates an enabled rule whose conditions must all match.
func (c *Client) CreateRule(spec RuleSpec) (*Rule, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	var res struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := c.runJXAJSON(buildCreateRuleScript(spec), &res); err != nil {
		return nil, err
	}
	return &Rule{Name: res.Name, Enabled: res.Enabled}, nil
}

func buildDeleteRuleScript(name string) string {
	return jsMain(fmt.Sprintf(`
	const ruleName = %s;
	const names = mail.rules.name();
	const hits = [];
	for (let i = 0; i < names.length; i++) if (names[i] === ruleName) hits.push(i);
	if (hits.length === 0) throw 'Rule not found: ' + ruleName;
	if (hits.length > 1) throw 'Multiple rules are named ' + ruleName + '; rename one in Mail.app first';
	mail.delete(mail.rules[hits[0]]);
	if (mail.rules.name().indexOf(ruleName) >= 0) throw 'Mail.app accepted the delete command but the rule still exists';
	return { name: ruleName };
`, jsQuote(name)))
}

// DeleteRule deletes the single rule with exactly this name.
func (c *Client) DeleteRule(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("rule name must not be empty")
	}
	return c.runJXAJSON(buildDeleteRuleScript(name), nil)
}

// ---------------------------------------------------------------------------
// Attachments, drafts, reply/forward with attachments
// ---------------------------------------------------------------------------

// ResolveAttachmentPaths checks every path is an existing regular file and
// returns absolute paths. Mail silently drops attachments it cannot read,
// so a typo would otherwise send a message without the file.
func ResolveAttachmentPaths(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("attachment %q: %w", p, err)
		}
		st, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("attachment %q: %w", p, err)
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("attachment %q is not a regular file", p)
		}
		out = append(out, abs)
	}
	return out, nil
}

// appleScriptAttachLines is the attachment mechanism `send --attach` uses
// (`make new attachment ... at after the last paragraph` inside a
// `tell <outgoing message>` block). With strict set, a failure aborts the
// script instead of being skipped.
func appleScriptAttachLines(paths []string, strict bool) string {
	var b strings.Builder
	for _, p := range paths {
		onError := "-- Skip files that can't be attached"
		if strict {
			onError = fmt.Sprintf(`error "could not attach " & %s & ": " & errMsg`, asQuote(p))
		}
		fmt.Fprintf(&b, `
			try
				make new attachment with properties {file name:%s} at after the last paragraph
			on error errMsg
				%s
			end try
`, asQuote(p), onError)
	}
	return b.String()
}

// buildAttachAndSendAppleScript attaches files to an already-prepared
// outgoing message (looked up by id) and sends it. If anything fails the
// unsent message is closed without saving so it does not linger.
func buildAttachAndSendAppleScript(outgoingID int, paths []string) string {
	return fmt.Sprintf(`tell application "Mail"
	set m to missing value
	try
		set m to first outgoing message whose id is %d
		tell m
%s
		end tell
		send m
		return "Success"
	on error errMsg
		try
			if m is not missing value then close m saving no
		end try
		return "Error: " & errMsg
	end try
end tell`, outgoingID, appleScriptAttachLines(paths, true))
}

func (c *Client) attachAndSend(outgoingID int, paths []string) error {
	out, err := c.runAppleScript(buildAttachAndSendAppleScript(outgoingID, paths))
	if err != nil {
		return err
	}
	if strings.HasPrefix(out, "Error: ") {
		return errors.New(out)
	}
	return nil
}

// OutgoingMessage holds the fields for composing a new message.
type OutgoingMessage struct {
	Account     string
	Subject     string
	Body        string
	To          []string
	Cc          []string
	Bcc         []string
	Attachments []string
	Signature   string
}

func asList(addrs []string) string {
	esc := make([]string, len(addrs))
	for i, a := range addrs {
		esc[i] = escapeAppleScriptString(a)
	}
	return strings.Join(esc, `", "`)
}

// buildOutgoingAppleScript composes a new outgoing message; finalCmd is the
// statement run inside `tell newMessage` once it is populated ("send" or
// "save newMessage"). strictAttach makes attachment failures abort.
func buildOutgoingAppleScript(m OutgoingMessage, finalCmd string, strictAttach bool) string {
	toList, ccList, bccList := asList(m.To), asList(m.Cc), asList(m.Bcc)
	signatureCode := ""
	if m.Signature != "" {
		signatureCode = fmt.Sprintf(`
				try
					set message signature to signature "%s"
				on error
					-- Unknown signature name: send without one rather than fail
				end try
`, escapeAppleScriptString(m.Signature))
	}
	return fmt.Sprintf(`
	tell application "Mail"
		try
			set targetAccount to account "%s"
			set newMessage to make new outgoing message with properties {subject:"%s", content:"%s", visible:false}

			tell newMessage
				set sender to (item 1 of (email addresses of targetAccount as list))

				repeat with addr in {"%s"}
					make new to recipient at end of to recipients with properties {address:addr}
				end repeat

				if "%s" is not "" then
					repeat with addr in {"%s"}
						make new cc recipient at end of cc recipients with properties {address:addr}
					end repeat
				end if

				if "%s" is not "" then
					repeat with addr in {"%s"}
						make new bcc recipient at end of bcc recipients with properties {address:addr}
					end repeat
				end if
%s
%s
			%s
			end tell
			return "Success"
		on error errMsg
			return "Error: " & errMsg
		end try
	end tell
`, escapeAppleScriptString(m.Account), escapeAppleScriptString(m.Subject), escapeAppleScriptString(m.Body),
		toList,
		ccList, ccList,
		bccList, bccList,
		signatureCode,
		appleScriptAttachLines(m.Attachments, strictAttach),
		finalCmd)
}

func buildDraftsSnapshotScript(accountName string) string {
	return jsMain(fmt.Sprintf(`
	const acc = getAccount(%s);
	if (isLocalAccount(acc)) throw 'Drafts need a sending account, not ' + acc.name();
	const candidates = [%s];
	let box = null;
	for (let i = 0; i < candidates.length && !box; i++) box = resolveMailbox(acc, candidates[i]);
	if (!box) throw 'Drafts mailbox not found for account ' + acc.name();
	return { account: acc.name(), mailbox: box.name(), ids: box.messages.id().map(String) };
`, jsQuote(accountName), jsCandidateList("drafts")))
}

func jsCandidateList(kind string) string {
	names := specialMailboxCandidates[kind]
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = jsQuote(n)
	}
	return strings.Join(q, ", ")
}

func buildDraftsPollScript(accountName, mailboxName, subject string, before []string, timeoutSec int) string {
	quoted := make([]string, len(before))
	for i, id := range before {
		quoted[i] = jsQuote(id)
	}
	beforeJS := "[" + strings.Join(quoted, ", ") + "]"
	return jsMain(fmt.Sprintf(`
	const acc = getAccount(%s);
	const box = resolveMailbox(acc, %s);
	if (!box) throw 'Drafts mailbox disappeared';
	const before = {};
	%s.forEach(id => { before[id] = true; });
	const subject = %s;
	for (let t = 0; t < %d * 2; t++) {
		const ids = box.messages.id();
		for (let i = 0; i < ids.length; i++) {
			if (before[String(ids[i])]) continue;
			const msg = box.messages.at(i);
			if ((msg.subject() || '') === subject) {
				return { found: true, id: String(ids[i]), subject: subject, mailbox: box.name(), account: acc.name() };
			}
		}
		delay(0.5);
	}
	return { found: false };
`, jsQuote(accountName), jsQuote(mailboxName), beforeJS, jsQuote(subject), timeoutSec))
}

// draftPollTimeoutSec bounds how long SaveDraft waits for the saved draft to
// appear in the account's Drafts mailbox.
const draftPollTimeoutSec = 15

// SaveDraft composes the message exactly like SendMessage but runs `save`
// instead of `send`, then confirms that a new message with the same subject
// appeared in the account's Drafts mailbox. A save that Mail silently
// ignores (headless compose) is therefore reported as an error, not success.
func (c *Client) SaveDraft(m OutgoingMessage) (*Message, error) {
	var snap struct {
		Account string   `json:"account"`
		Mailbox string   `json:"mailbox"`
		IDs     []string `json:"ids"`
	}
	if err := c.runJXAJSON(buildDraftsSnapshotScript(m.Account), &snap); err != nil {
		return nil, err
	}
	out, err := c.runAppleScript(buildOutgoingAppleScript(m, "save newMessage", true))
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(out, "Error: ") {
		return nil, errors.New(out)
	}
	var found struct {
		Found   bool   `json:"found"`
		ID      string `json:"id"`
		Subject string `json:"subject"`
		Mailbox string `json:"mailbox"`
		Account string `json:"account"`
	}
	if err := c.runJXAJSON(buildDraftsPollScript(m.Account, snap.Mailbox, m.Subject, snap.IDs, draftPollTimeoutSec), &found); err != nil {
		return nil, err
	}
	if !found.Found {
		return nil, fmt.Errorf("Mail.app accepted the save command but no draft with subject %q appeared in %s/%s within %ds — headless draft save did not work on this Mail.app", m.Subject, snap.Account, snap.Mailbox, draftPollTimeoutSec)
	}
	return &Message{ID: found.ID, Subject: found.Subject, Mailbox: found.Mailbox, Account: found.Account, FlagIndex: -1}, nil
}

// ---------------------------------------------------------------------------
// Open in Mail.app
// ---------------------------------------------------------------------------

func buildOpenMessageScript(accountName, mailboxName, messageID string) string {
	return jsMain(fmt.Sprintf(`
	const acc = getAccount(%s);
	const mbox = resolveMailbox(acc, %s);
	if (!mbox) throw 'Mailbox not found';
	const idx = mbox.messages.id().findIndex(id => String(id) === %s);
	if (idx < 0) throw 'Message not found';
	mail.activate();
	mail.open(mbox.messages.at(idx));
	return { opened: true };
`, jsQuote(accountName), jsQuote(mailboxName), jsQuote(messageID)))
}

// OpenMessage brings Mail.app to the front with the message open in its own
// window. A viewer's "selected messages" is not used because setting it was
// observed to land one row off the requested message (see ArchiveMessage
// history), while `open` on the message object is exact.
func (c *Client) OpenMessage(accountName, mailboxName, messageID string) error {
	return c.runJXAJSON(buildOpenMessageScript(accountName, mailboxName, messageID), nil)
}
