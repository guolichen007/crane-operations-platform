package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

func sample(name string) Input {
	return Input{ProjectName: name, Customer: "Synthetic Example 有限公司", Location: Location{Province: "合成省", City: "合成市", FactoryName: "合成一厂"}, SalesStage: "L1_REQUIREMENT", ProjectType: "RETROFIT", InformationSource: "END_CUSTOMER", ConfidenceLevel: "CUSTOMER_CONFIRMED", Requirements: []string{"ANTI_SWAY"}, RequirementNotes: "synthetic 需求", PlannedEquipment: []Equipment{{EquipmentType: "BRIDGE", Quantity: pointer(2)}}, ExpectedPurchaseDate: "2027-01-01"}
}
func TestNormalization(t *testing.T) {
	for _, pair := range [][2]string{{" ＡＢＣ， 有限公司  ", "abc 有限公司"}, {"示例（一期）\t项目", "示例 一期 项目"}, {"Hello---World", "hello world"}} {
		if got := normalize(pair[0]); got != pair[1] {
			t.Fatalf("%q", got)
		}
	}
	if normalize("示例有限公司") == normalize("示例") {
		t.Fatal("company suffix removed")
	}
}
func TestDuplicateScoring(t *testing.T) {
	p := sample("合成改造")
	q := p
	if n := score(p, q, 1, true); n < 90 {
		t.Fatal(n)
	}
	q.Customer = "Unrelated"
	q.Location.FactoryName = "other"
	if n := score(p, q, 0, false); n >= 70 {
		t.Fatal(n)
	}
	p.TenderNo = "ＳＹＮ-001"
	q.TenderNo = "syn 001"
	if score(p, q, 0, false) != 100 {
		t.Fatal("exact tender")
	}
	if fingerprint(p) != fingerprint(p) {
		t.Fatal("unstable fingerprint")
	}
}
func TestRBACOwnership(t *testing.T) {
	p := Project{OwnerSubject: "a", TeamID: "one"}
	cases := []struct {
		u           User
		read, write bool
	}{{User{Subject: "a", Role: "baobei_sales"}, true, true}, {User{Subject: "b", Role: "baobei_sales", TeamID: "one"}, false, false}, {User{Role: "baobei_manager", TeamID: "one"}, true, false}, {User{Role: "baobei_manager", TeamID: "two"}, false, false}, {User{Role: "baobei_manager"}, false, false}, {User{Role: "baobei_director"}, true, false}, {User{Role: "baobei_admin"}, true, true}}
	for _, c := range cases {
		if canRead(c.u, p) != c.read || canWrite(c.u, p) != c.write {
			t.Fatalf("RBAC %v", c.u)
		}
	}
}
func TestStatusAndValidation(t *testing.T) {
	for _, s := range []string{"DRAFT", "RETURNED"} {
		if !editable(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"SUBMITTED", "UNDER_REVIEW", "REJECTED", "CONVERTED"} {
		if editable(s) {
			t.Fatal(s)
		}
	}
	p := sample("合成项目")
	if e := validateInput(&p, true); e != nil {
		t.Fatal(e)
	}
	p.Customer = ""
	if validateInput(&p, true) == nil {
		t.Fatal("missing customer")
	}
	p = sample("合成项目")
	p.Requirements = []string{"SAFETY", "SAFETY"}
	if validateInput(&p, true) == nil {
		t.Fatal("duplicate category")
	}
	p = sample(strings.Repeat("x", 601))
	if validateInput(&p, false) == nil {
		t.Fatal("length")
	}
}
func TestUnconnectedAdapter(t *testing.T) {
	p, e := (UnconnectedAdapter{}).Validate(context.Background(), nil)
	if e == nil || p.Connected {
		t.Fatal("stub claims integration")
	}
}
func TestProductionConfig(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DEV_AUTH", "1")
	if _, e := config(); e == nil {
		t.Fatal("production dev auth accepted")
	}
	t.Setenv("APP_ENV", "development")
	t.Setenv("LISTEN_ADDR", "0.0.0.0:8088")
	if _, e := config(); e == nil {
		t.Fatal("public dev auth accepted")
	}
}

func TestProductionConfigGate(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DEV_AUTH", "0")
	t.Setenv("PUBLIC_ORIGIN", "https://baobei.youdaozd.com")
	t.Setenv("OIDC_ISSUER", "https://crane.youdaozd.com/realms/crane")
	t.Setenv("OIDC_CLIENT_ID", "baobei-web")
	t.Setenv("OIDC_CLIENT_SECRET", "synthetic-unit-only")
	t.Setenv("OIDC_CLIENT_SECRET_FILE", "")
	t.Setenv("BAOBEI_IDENTITY_REALM", "crane")
	t.Setenv("BAOBEI_ACCOUNT_CLIENT_ID", "")
	if _, e := config(); e != nil {
		t.Fatal(e)
	}
	for _, pair := range [][2]string{{"DEV_AUTH", "1"}, {"PUBLIC_ORIGIN", "http://bad"}, {"OIDC_ISSUER", "http://issuer"}, {"OIDC_CLIENT_ID", "other-client"}} {
		old := os.Getenv(pair[0])
		t.Setenv(pair[0], pair[1])
		if _, e := config(); e == nil {
			t.Fatal(pair[0])
		}
		t.Setenv(pair[0], old)
	}
}
