package main

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// V1.1 R7-D: team-safe customer names (no other team's spelling anywhere) and
// one version for every display-name change.

func TestDatabaseV11R7CustomerNamePrivacy(t *testing.T) {
	f := g1FixtureFor(t)
	ctx := context.Background()
	a1, a2, ma, d1 := f.sales, f.other, f.manager, f.director
	c1, mb := login(t, f.server, "synthetic-c"), login(t, f.server, "synthetic-manager-other")
	tag := strings.ToLower(randomToken()[:5])
	// Team one's original spelling (distinctive case and punctuation).
	spellA := "R7Tag" + tag + "·北岭重工（集团）有限公司"
	// The same normalized name, typed differently by team two.
	spellB := "r7tag" + tag + " 北岭重工 集团 有限公司"
	if normalize(spellA) != normalize(spellB) {
		t.Fatal("fixture spellings must normalize equally", normalize(spellA), normalize(spellB))
	}
	in := sample("R7名称A" + tag)
	in.Customer = spellA
	s, b := a1.call("POST", "/api/v1/projects", in)
	pa := projectFrom(t, s, b)
	// Legacy path: team two files a project for the same master record (no
	// team profile anywhere for team two).
	in2 := sample("R7名称B" + tag)
	in2.Customer = spellB
	in2.Location.FactoryName = "另一厂区" + tag
	s, b = c1.call("POST", "/api/v1/projects", in2)
	pb := projectFrom(t, s, b)
	var orgs int
	_ = f.db.QueryRow(ctx, `SELECT count(*) FROM organizations WHERE normalized_name=$1`, normalize(spellA)).Scan(&orgs)
	if orgs != 1 {
		t.Fatal("master record duplicated", orgs)
	}
	leaks := func(label string, body []byte, forbidden string) {
		t.Helper()
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("%s leaks %q: %s", label, forbidden, body)
		}
	}
	// Every customer surface for team two: list, search (several spellings),
	// detail, similar, create, project contacts — never team one's spelling.
	var id string
	for _, q := range []string{tag, "北岭", "集团", "R7TAG" + tag, "%", "_", `\`} {
		s, b = c1.call("GET", "/api/v1/customers?q="+urlQuery(q), nil)
		expect(t, s, 200, b)
		leaks("list q="+q, b, spellA)
		var l struct {
			Items []CustomerItem `json:"items"`
		}
		_ = json.Unmarshal(b, &l)
		for _, it := range l.Items {
			if strings.Contains(it.Name, tag) {
				id = it.ID
				if it.Name != spellB {
					t.Fatalf("team two sees %q, want its own spelling %q", it.Name, spellB)
				}
			}
		}
		if q == "%" || q == "_" || q == `\` {
			// Wildcards are literal: no customer name here contains them.
			for _, it := range l.Items {
				if strings.Contains(it.Name, tag) {
					t.Fatalf("wildcard %q matched %q", q, it.Name)
				}
			}
		}
	}
	if id == "" {
		t.Fatal("team two must find the customer of its own project")
	}
	s, b = c1.call("GET", "/api/v1/customers/"+id, nil)
	expect(t, s, 200, b)
	leaks("detail", b, spellA)
	s, b = c1.call("GET", "/api/v1/customers/similar?name="+urlQuery(spellB), nil)
	expect(t, s, 200, b)
	leaks("similar", b, spellA)
	s, b = c1.call("GET", "/api/v1/projects/"+pb.ID+"/contacts", nil)
	expect(t, s, 200, b)
	leaks("project contacts", b, spellA)
	if !strings.Contains(string(b), spellB) {
		t.Fatal("project contacts must name the project's own spelling", string(b))
	}
	s, b = mb.call("POST", "/api/v1/customers", map[string]any{"name": "R7TAG" + tag + "-北岭重工(集团)有限公司"})
	expect(t, s, 200, b)
	leaks("create", b, spellA)
	leaks("create", b, spellB)
	// Team one keeps its own spelling everywhere.
	s, b = a1.call("GET", "/api/v1/projects/"+pa.ID+"/contacts", nil)
	expect(t, s, 200, b)
	leaks("team one project contacts", b, spellB)
	s, b = ma.call("GET", "/api/v1/customers?q="+tag, nil)
	expect(t, s, 200, b)
	leaks("team one list", b, spellB)
	// Director sees the master spelling (the first registered one).
	s, b = d1.call("GET", "/api/v1/customers/"+id, nil)
	expect(t, s, 200, b)
	if !strings.Contains(string(b), spellA) {
		t.Fatal("director master spelling", string(b))
	}
	// Transfer team one's project to team two? Transfers stay inside a team;
	// moving A1's project to A2 (same team) keeps the team-one spelling, and
	// A1 (no longer owner, no team profile) no longer finds the customer.
	cur := v11Get(t, ma, pa.ID)
	s, b = g1Decision(ma, cur, "ownership-transfer", map[string]any{"targetSubject": "synthetic-b"})
	expect(t, s, 200, b)
	s, b = a1.call("GET", "/api/v1/customers?q="+tag, nil)
	expect(t, s, 200, b)
	if strings.Contains(string(b), tag) {
		t.Fatal("previous owner still lists the customer", string(b))
	}
	s, b = a2.call("GET", "/api/v1/customers?q="+tag, nil)
	expect(t, s, 200, b)
	if !strings.Contains(string(b), spellA) {
		t.Fatal("new owner must see its project's spelling", string(b))
	}
	// Filed texts are never rewritten.
	var docA, docB string
	_ = f.db.QueryRow(ctx, `SELECT document->>'customer' FROM intake_projects WHERE id=$1`, pa.ID).Scan(&docA)
	_ = f.db.QueryRow(ctx, `SELECT document->>'customer' FROM intake_projects WHERE id=$1`, pb.ID).Scan(&docB)
	if docA != spellA || docB != spellB {
		t.Fatal("filed customer text changed", docA, docB)
	}
}

func urlQuery(s string) string {
	r := strings.NewReplacer("%", "%25", "&", "%26", "+", "%2B", " ", "%20", "#", "%23", `\`, "%5C", "·", "%C2%B7")
	return r.Replace(s)
}

// Stale own-profile submission after a staff correction, and the reverse.
func TestDatabaseV11R7ProfileVersion(t *testing.T) {
	f := g1FixtureFor(t)
	ctx := context.Background()
	b := login(t, f.server, "synthetic-b")
	read := func() ownProfile {
		s, body := b.call("GET", "/api/v1/me/profile", nil)
		expect(t, s, 200, body)
		var p ownProfile
		_ = json.Unmarshal(body, &p)
		return p
	}
	identity := func() string {
		var v string
		_ = f.db.QueryRow(ctx, `SELECT subject||'/'||role||'/'||coalesce(team_id::text,'') FROM app_users WHERE subject='synthetic-b'`).Scan(&v)
		return v
	}
	id0 := identity()
	page := read() // the member opens the profile page
	// The supervisor corrects the name meanwhile.
	var staffVersion int
	_ = f.db.QueryRow(ctx, `SELECT staff_version FROM app_users WHERE subject='synthetic-b'`).Scan(&staffVersion)
	s, body := f.manager.call("PATCH", "/api/v1/staff/"+staffIDOf(t, f.db, "synthetic-b"), map[string]any{"name": "主管更正的姓名", "phone": "", "email": "synthetic-b@example.test", "teamId": syntheticTeam, "role": salesRole, "reason": "更正", "version": staffVersion})
	expect(t, s, 200, body)
	after := read()
	if after.Version != page.Version+1 || after.Name != "主管更正的姓名" {
		t.Fatalf("staff rename must advance profile_version once: %d -> %d", page.Version, after.Version)
	}
	// The member submits from the stale page: conflict, correction kept.
	s, body = b.call("PATCH", "/api/v1/me/profile", map[string]any{"name": "本人旧页面", "reason": "", "version": page.Version})
	expect(t, s, 409, body)
	if read().Name != "主管更正的姓名" {
		t.Fatal("stale submission overwrote the correction")
	}
	// Reverse: the supervisor works from a stale staff version after the
	// member's own change.
	cur := read()
	s, body = b.call("PATCH", "/api/v1/me/profile", map[string]any{"name": "本人新姓名", "reason": "", "version": cur.Version})
	expect(t, s, 200, body)
	self := read()
	if self.Version != cur.Version+1 {
		t.Fatal("own change must advance exactly once", cur.Version, self.Version)
	}
	_ = f.db.QueryRow(ctx, `SELECT staff_version FROM app_users WHERE subject='synthetic-b'`).Scan(&staffVersion)
	s, body = f.manager.call("PATCH", "/api/v1/staff/"+staffIDOf(t, f.db, "synthetic-b"), map[string]any{"name": "主管覆盖", "phone": "", "email": "synthetic-b@example.test", "teamId": syntheticTeam, "role": salesRole, "reason": "旧页面", "version": staffVersion - 1})
	expect(t, s, 409, body)
	if read().Name != "本人新姓名" {
		t.Fatal("stale staff edit overwrote the member's change")
	}
	// Staff edit that does not change the name keeps profile_version.
	_ = f.db.QueryRow(ctx, `SELECT staff_version FROM app_users WHERE subject='synthetic-b'`).Scan(&staffVersion)
	s, body = f.manager.call("PATCH", "/api/v1/staff/"+staffIDOf(t, f.db, "synthetic-b"), map[string]any{"name": "本人新姓名", "phone": "", "email": "synthetic-b@example.test", "teamId": syntheticTeam, "role": salesRole, "note": "备注", "reason": "备注", "version": staffVersion})
	expect(t, s, 200, body)
	if read().Version != self.Version {
		t.Fatal("unchanged name advanced profile_version")
	}
	// Audit: every change recorded with its version; identity unchanged.
	rows, err := f.db.Query(ctx, `SELECT a.action,a.new_name,a.profile_version FROM profile_audit a JOIN app_users u ON u.staff_id=a.staff_id WHERE u.subject='synthetic-b' ORDER BY a.at`)
	if err != nil {
		t.Fatal(err)
	}
	var trail []string
	versions := map[int]bool{}
	for rows.Next() {
		var action, name string
		var v int
		_ = rows.Scan(&action, &name, &v)
		trail = append(trail, action+":"+name)
		if versions[v] {
			t.Fatal("two audit rows with the same profile version", v, trail)
		}
		versions[v] = true
	}
	rows.Close()
	if strings.Join(trail, ",") != "profile.staff-edit:主管更正的姓名,profile.rename:本人新姓名" {
		t.Fatal("audit trail", trail)
	}
	if identity() != id0 {
		t.Fatal("identity changed by profile operations")
	}
}

// AC-23 for the new writes: concurrent duplicate submissions and retries do
// not create duplicate business rows; archive and link interleave safely.
func TestDatabaseV11R7WriteRaces(t *testing.T) {
	f := g1FixtureFor(t)
	ctx := context.Background()
	in := sample("V11R7竞态")
	in.Customer = "V11R7竞态客户"
	s, b := f.sales.call("POST", "/api/v1/projects", in)
	p := g1Submit(t, f.sales, projectFrom(t, s, b))
	var cust string
	if err := f.db.QueryRow(ctx, `SELECT organization_id::text FROM project_parties WHERE project_id=$1 AND role='END_CUSTOMER'`, p.ID).Scan(&cust); err != nil {
		t.Fatal(err)
	}
	parallel := func(n int, fn func() int) map[int]int {
		out := map[int]int{}
		ch := make(chan int, n)
		for i := 0; i < n; i++ {
			go func() { ch <- fn() }()
		}
		for i := 0; i < n; i++ {
			out[<-ch]++
		}
		return out
	}
	// 8 identical contact creates at once (double click + retries).
	codes := parallel(8, func() int {
		s, _ := f.sales.call("POST", "/api/v1/customers/"+cust+"/contacts", map[string]any{"name": "竞态联系人", "phone": "13700000001"})
		return s
	})
	if codes[200] != 8 || countRows(t, f.db, `SELECT count(*) FROM contacts WHERE display_name='竞态联系人'`) != 1 ||
		countRows(t, f.db, `SELECT count(*) FROM customer_audit WHERE action='contact.create' AND details->>'name'='竞态联系人'`) != 1 {
		t.Fatal("duplicate contact", codes)
	}
	// 8 identical questions at once.
	codes = parallel(8, func() int {
		s, _ := f.manager.call("POST", "/api/v1/projects/"+p.ID+"/technical-brief/questions", map[string]any{"question": "竞态问题？"})
		return s
	})
	if codes[200] != 8 || countRows(t, f.db, `SELECT count(*) FROM project_technical_questions WHERE question='竞态问题？'`) != 1 ||
		countRows(t, f.db, `SELECT count(*) FROM project_technical_question_events WHERE text='竞态问题？'`) != 1 {
		t.Fatal("duplicate question", codes)
	}
	// 6 concurrent brief saves from the same version: exactly one wins.
	codes = parallel(6, func() int {
		s, _ := f.sales.call("PUT", "/api/v1/projects/"+p.ID+"/technical-brief", map[string]any{"version": 0, "status": "COLLECTING", "scenario": "并发"})
		return s
	})
	if codes[200] != 1 || codes[409] != 5 || countRows(t, f.db, `SELECT count(*) FROM project_technical_brief_revisions WHERE project_id='`+p.ID+`'`) != 1 {
		t.Fatal("brief lost update", codes)
	}
	// Archive and link interleaved, many rounds: never an active link to an
	// archived contact created after the archive, and every response is a
	// clean 200/404/409.
	for round := 0; round < 10; round++ {
		s, b = f.sales.call("POST", "/api/v1/customers/"+cust+"/contacts", map[string]any{"name": "交错" + string(rune('A'+round)), "phone": ""})
		expect(t, s, 200, b)
		var c Contact
		for _, x := range customerOf(t, f.sales, cust).Contacts {
			if x.Name == "交错"+string(rune('A'+round)) {
				c = x
			}
		}
		cur := v11Get(t, f.sales, p.ID)
		done := make(chan int, 2)
		go func() {
			s, _ := f.sales.call("POST", "/api/v1/contacts/"+c.ID+"/archive", map[string]any{"name": c.Name, "version": c.Version, "reason": "离职"})
			done <- s
		}()
		go func() {
			s, _ := f.sales.call("POST", "/api/v1/projects/"+p.ID+"/contacts", map[string]any{"contactId": c.ID, "version": cur.Version})
			done <- s
		}()
		a, l := <-done, <-done
		for _, s := range []int{a, l} {
			if s != 200 && s != 404 && s != 409 {
				t.Fatal("unexpected status", a, l)
			}
		}
		var linkedAfterArchive int
		_ = f.db.QueryRow(ctx, `SELECT count(*) FROM project_contacts pc JOIN contacts c ON c.id=pc.contact_id WHERE c.id=$1 AND c.archived_at IS NOT NULL AND pc.linked_at>c.archived_at`, c.ID).Scan(&linkedAfterArchive)
		if linkedAfterArchive != 0 {
			t.Fatal("link created after archive")
		}
	}
	// Transfer interleaved with a contact link by the previous owner: the
	// link is refused after the transfer (owner check on the locked row).
	cur := v11Get(t, f.sales, p.ID)
	s, b = f.sales.call("POST", "/api/v1/customers/"+cust+"/contacts", map[string]any{"name": "移交前联系人", "phone": ""})
	expect(t, s, 200, b)
	var c Contact
	for _, x := range customerOf(t, f.sales, cust).Contacts {
		if x.Name == "移交前联系人" {
			c = x
		}
	}
	s, b = g1Decision(f.manager, v11Get(t, f.manager, p.ID), "ownership-transfer", map[string]any{"targetSubject": "synthetic-b"})
	expect(t, s, 200, b)
	s, b = f.sales.call("POST", "/api/v1/projects/"+p.ID+"/contacts", map[string]any{"contactId": c.ID, "version": cur.Version + 1})
	if s != 404 && s != 403 {
		t.Fatal("previous owner linked after transfer", s, string(b))
	}
}

// The backup/restore tool (tests/baobei/backup.mjs) refuses any table it does
// not list. Every table created by the embedded migrations must be listed, so
// a new migration cannot silently break the CI restore test (Run 44027107200).
func TestDatabaseV11R7BackupTableList(t *testing.T) {
	db := b2Database(t)
	src, err := os.ReadFile("../../tests/baobei/backup.mjs")
	if err != nil {
		t.Skip("NOT_RUN: repository checkout required", err)
	}
	block := string(src)
	start := strings.Index(block, "const productTables = [")
	end := strings.Index(block[start:], "].sort();")
	if start < 0 || end < 0 {
		t.Fatal("backup.mjs table list not found")
	}
	listed := map[string]bool{}
	for _, m := range regexp.MustCompile(`'([a-z_0-9]+)'`).FindAllStringSubmatch(block[start:start+end], -1) {
		listed[m[1]] = true
	}
	rows, err := db.Query(context.Background(), `SELECT tablename FROM pg_tables WHERE schemaname=current_schema() ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var name string
		_ = rows.Scan(&name)
		n++
		if !listed[name] {
			t.Errorf("table %s is missing from tests/baobei/backup.mjs productTables", name)
		}
		delete(listed, name)
	}
	if len(listed) != 0 {
		t.Errorf("backup.mjs lists tables the schema does not have: %v", listed)
	}
	if n < 40 {
		t.Fatal("schema too small", n)
	}
}
