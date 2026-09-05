package ecosystemalias

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	primitives "github.com/bsv-blockchain/go-sdk/primitives/ec"
)

func loadFixture(t *testing.T) (map[string]any, []byte) {
	t.Helper()
	path := filepath.Join("testdata", "brc169-aliases.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	return doc, raw
}

func fixtureObject(t *testing.T, doc map[string]any, keys ...string) map[string]any {
	t.Helper()
	cur := any(doc)
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("not an object at %s", k)
		}
		cur, ok = m[k]
		if !ok {
			t.Fatalf("missing %s", k)
		}
	}
	m, ok := cur.(map[string]any)
	if !ok {
		t.Fatalf("not an object: %v", keys)
	}
	return m
}

func fixtureSlice(t *testing.T, doc map[string]any, keys ...string) []any {
	t.Helper()
	cur := any(doc)
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("not an object at %s", k)
		}
		cur, ok = m[k]
		if !ok {
			t.Fatalf("missing %s", k)
		}
	}
	s, ok := cur.([]any)
	if !ok {
		t.Fatalf("not an array: %v", keys)
	}
	return s
}

func asString(t *testing.T, v any) string {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("not a string: %T", v)
	}
	return s
}

func asNumberInt(t *testing.T, v any) int64 {
	t.Helper()
	n, ok := v.(json.Number)
	if !ok {
		t.Fatalf("not a number: %T", v)
	}
	i, err := n.Int64()
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func asFloat(t *testing.T, v any) float64 {
	t.Helper()
	n, ok := v.(json.Number)
	if !ok {
		t.Fatalf("not a number: %T", v)
	}
	f, err := n.Float64()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestFixtureCanonicalSHA256(t *testing.T) {
	doc, _ := loadFixture(t)
	gotStored := asString(t, doc["canonicalSha256"])
	delete(doc, "canonicalSha256")
	canon, err := canonicalJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canon)
	got := hex.EncodeToString(sum[:])
	if gotStored != got {
		t.Fatalf("canonicalSha256 drift: stored %s computed %s", gotStored, got)
	}
}

func TestConstants(t *testing.T) {
	doc, _ := loadFixture(t)
	c := fixtureObject(t, doc, "constants")
	if TopicName != asString(t, c["topic"]) || LookupName != asString(t, c["lookup"]) {
		t.Fatalf("topic/lookup mismatch")
	}
	if ProtocolName != asString(t, c["protocol"]) || ProtocolVersion != asString(t, c["version"]) {
		t.Fatalf("protocol mismatch")
	}
	if FieldCount != int(asNumberInt(t, c["fieldCount"])) {
		t.Fatalf("field count")
	}
	if DefaultLimit != uint32(asNumberInt(t, c["defaultLimit"])) || MaxLimit != uint32(asNumberInt(t, c["maxLimit"])) {
		t.Fatalf("limits")
	}
	if LookupHTTPPath != asString(t, c["httpLookupPath"]) {
		t.Fatalf("http path")
	}
}

func TestSigmaTransactionNotClaimed(t *testing.T) {
	doc, raw := loadFixture(t)
	notes := fixtureObject(t, doc, "notes")
	msg := asString(t, notes["sigmaTransaction"])
	if !strings.Contains(strings.ToLower(msg), "does not include") {
		t.Fatalf("must document that no confirmed Sigma transaction is reproduced")
	}
	if bytes.Contains(bytes.ToLower(raw), []byte("confirmed sigma transaction bytes")) {
		t.Fatal("fixture must not claim confirmed Sigma transaction bytes")
	}
}

func TestPositiveTokens(t *testing.T) {
	doc, _ := loadFixture(t)
	signing := fixtureObject(t, doc, "signing")
	privHex := asString(t, signing["privateKeyHex"])
	priv, err := primitives.PrivateKeyFromHex(privHex)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range fixtureSlice(t, doc, "tokens", "positive") {
		row := item.(map[string]any)
		name := asString(t, row["name"])
		t.Run(name, func(t *testing.T) {
			alias := asString(t, row["alias"])
			domain := asString(t, row["domain"])
			key, err := hex.DecodeString(asString(t, row["certifierKeyHex"]))
			if err != nil {
				t.Fatal(err)
			}
			sig, err := hex.DecodeString(asString(t, row["signatureDerHex"]))
			if err != nil {
				t.Fatal(err)
			}
			fields := [][]byte{
				[]byte(asString(t, row["protocol"])),
				[]byte(asString(t, row["version"])),
				[]byte(alias),
				[]byte(domain),
				key,
				sig,
			}
			claim, err := ValidateTokenFields(fields)
			if err != nil {
				t.Fatal(err)
			}
			if claim.Alias != alias || claim.Domain != domain {
				t.Fatalf("claim identity %s %s", claim.Alias, claim.Domain)
			}
			if err := ValidateSats(uint64(asNumberInt(t, row["satoshis"]))); err != nil {
				t.Fatal(err)
			}
			var cert [33]byte
			copy(cert[:], key)
			digest := Digest(alias, domain, cert)
			if pre, ok := row["preimageHex"].(string); ok {
				gotPre := hex.EncodeToString(Preimage(ProtocolName, ProtocolVersion, alias, domain, cert))
				if gotPre != pre {
					t.Fatalf("preimage\n got %s\nwant %s", gotPre, pre)
				}
			}
			if want, ok := row["digestHex"].(string); ok {
				got := hex.EncodeToString(digest[:])
				if got != want {
					t.Fatalf("digest\n got %s\nwant %s", got, want)
				}
				parsed, err := primitives.FromDER(sig)
				if err != nil {
					t.Fatal(err)
				}
				if !primitives.Verify(digest[:], parsed, &priv.PublicKey) {
					t.Fatal("signature does not verify")
				}
			}
		})
	}
}

func TestNegativeTokens(t *testing.T) {
	doc, _ := loadFixture(t)
	for _, item := range fixtureSlice(t, doc, "tokens", "negative") {
		row := item.(map[string]any)
		name := asString(t, row["name"])
		want := Code(asString(t, row["code"]))
		t.Run(name, func(t *testing.T) {
			if name == "field-count-five" {
				_, err := ValidateTokenFields([][]byte{{}, {}, {}, {}, {}})
				assertCode(t, err, want)
				return
			}
			if name == "zero-sats" {
				err := ValidateSats(uint64(asNumberInt(t, row["satoshis"])))
				assertCode(t, err, want)
				return
			}
			key, err := hex.DecodeString(asString(t, row["certifierKeyHex"]))
			if err != nil {
				t.Fatal(err)
			}
			sig, err := hex.DecodeString(asString(t, row["signatureDerHex"]))
			if err != nil {
				t.Fatal(err)
			}
			fields := [][]byte{
				[]byte(asString(t, row["protocol"])),
				[]byte(asString(t, row["version"])),
				[]byte(asString(t, row["alias"])),
				[]byte(asString(t, row["domain"])),
				key,
				sig,
			}
			_, err = ValidateTokenFields(fields)
			assertCode(t, err, want)
		})
	}
}

func TestNormalization(t *testing.T) {
	doc, _ := loadFixture(t)
	runNorm := func(kind string, fn func(string) (string, error)) {
		pos := fixtureSlice(t, doc, "normalization", kind, "positive")
		for _, item := range pos {
			row := item.(map[string]any)
			in := asString(t, row["input"])
			t.Run(kind+"/ok/"+in, func(t *testing.T) {
				got, err := fn(in)
				if err != nil {
					t.Fatal(err)
				}
				if got != asString(t, row["output"]) {
					t.Fatalf("got %q want %q", got, asString(t, row["output"]))
				}
			})
		}
		neg := fixtureSlice(t, doc, "normalization", kind, "negative")
		for _, item := range neg {
			row := item.(map[string]any)
			in := asString(t, row["input"])
			t.Run(kind+"/err/"+in, func(t *testing.T) {
				_, err := fn(in)
				assertCode(t, err, Code(asString(t, row["code"])))
			})
		}
	}
	runNorm("alias", NormalizeAliasQuery)
	runNorm("domain", NormalizeDomainQuery)
}

func TestDecodeQuery(t *testing.T) {
	doc, _ := loadFixture(t)
	for _, item := range fixtureSlice(t, doc, "queries", "positive") {
		row := item.(map[string]any)
		name := asString(t, row["name"])
		t.Run(name, func(t *testing.T) {
			q, err := DecodeQuery(json.RawMessage(asString(t, row["json"])))
			if err != nil {
				t.Fatal(err)
			}
			if q.Mode() != Mode(asString(t, row["mode"])) {
				t.Fatalf("mode %s", q.Mode())
			}
			if q.BindingValue() != asString(t, row["value"]) {
				t.Fatalf("value %s", q.BindingValue())
			}
			if q.PageLimit() != uint32(asNumberInt(t, row["limit"])) {
				t.Fatalf("limit %d", q.PageLimit())
			}
			var wantSkip uint32
			if _, ok := row["skip"]; ok {
				wantSkip = uint32(asNumberInt(t, row["skip"]))
			}
			if q.PageSkip() != wantSkip {
				t.Fatalf("skip %d", q.PageSkip())
			}
		})
	}
	for _, item := range fixtureSlice(t, doc, "queries", "negative") {
		row := item.(map[string]any)
		name := asString(t, row["name"])
		t.Run("neg/"+name, func(t *testing.T) {
			_, err := DecodeQuery(json.RawMessage(asString(t, row["json"])))
			assertCode(t, err, Code(asString(t, row["code"])))
		})
	}
}

func TestOrdering(t *testing.T) {
	doc, _ := loadFixture(t)
	ord := fixtureObject(t, doc, "ordering")
	var items []namedPlacement
	for _, item := range ord["items"].([]any) {
		row := item.(map[string]any)
		items = append(items, namedPlacement{
			id: asString(t, row["id"]),
			p: Placement{
				Score: asFloat(t, row["score"]),
				Vout:  uint32(asNumberInt(t, row["vout"])),
			},
		})
	}
	gotLookup := orderIDs(items, CompareLookup)
	wantLookup := stringSlice(t, ord["aliasDomain"].([]any))
	if !reflect.DeepEqual(gotLookup, wantLookup) {
		t.Fatalf("alias/domain order\n got %v\nwant %v", gotLookup, wantLookup)
	}
}

func TestTypedErrorCodesSeparateFromMessages(t *testing.T) {
	_, err := DecodeQuery(json.RawMessage(`{"findAll":false}`))
	if err == nil {
		t.Fatal("expected error")
	}
	code, ok := CodeOf(err)
	if !ok || code != CodeFindAllFalse {
		t.Fatalf("code %s", code)
	}
	if err.Error() == string(code) {
		t.Fatal("message should not be only the code")
	}
	if !strings.Contains(err.Error(), string(code)) {
		t.Fatal("Error() should still include the stable code")
	}
}

type namedPlacement struct {
	id string
	p  Placement
}

func orderIDs(items []namedPlacement, cmp func(a, b Placement) int) []string {
	out := append([]namedPlacement(nil), items...)
	sort.SliceStable(out, func(i, j int) bool {
		return cmp(out[i].p, out[j].p) < 0
	})
	ids := make([]string, len(out))
	for i := range out {
		ids[i] = out[i].id
	}
	return ids
}

func stringSlice(t *testing.T, in []any) []string {
	t.Helper()
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = asString(t, v)
	}
	return out
}

func assertCode(t *testing.T, err error, want Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s, got nil", want)
	}
	got, ok := CodeOf(err)
	if !ok || got != want {
		t.Fatalf("got %v (%s) want %s", err, got, want)
	}
}

func canonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		b, err := json.Marshal(x)
		if err != nil {
			return err
		}
		buf.Write(b)
	case json.Number:
		buf.WriteString(x.String())
	case float64:
		b, err := json.Marshal(x)
		if err != nil {
			return err
		}
		buf.Write(b)
	case []any:
		buf.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			if err := writeCanonical(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return err
		}
		buf.Write(b)
	}
	return nil
}

func TestSameBlockTieUsesTxIndexThenVout(t *testing.T) {
	a := Placement{Score: EventScore(100, 1), Vout: 1}
	b := Placement{Score: EventScore(100, 9), Vout: 0}
	if CompareLookup(a, b) >= 0 {
		t.Fatal("same-block must prefer the lower transaction index")
	}
	c := Placement{Score: EventScore(100, 1), Vout: 0}
	if CompareLookup(c, a) >= 0 {
		t.Fatal("same score must prefer the lower vout")
	}
}

func TestQuerySkipBounds(t *testing.T) {
	for _, raw := range []string{`{"findAll":true,"skip":4294967296}`, `{"findAll":true,"skip":9223372036854775807}`} {
		if _, err := DecodeQuery([]byte(raw)); err == nil {
			t.Fatalf("accepted overflowing skip: %s", raw)
		}
	}
	q, err := DecodeQuery([]byte(`{"findAll":true,"skip":4294967295}`))
	if err != nil || q.PageSkip() != 4294967295 {
		t.Fatalf("max uint32 skip: %v, %v", q, err)
	}
}
