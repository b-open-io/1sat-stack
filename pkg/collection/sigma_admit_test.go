package collection

import (
	"encoding/base64"
	"testing"

	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Olive test root, acb61696…, was submitted and rejected by the go-sigma double hash.
const oliveTestRaw = "AQAAAAFvMf+yLVVzTZlNAxRWcZxe744o6xhlE66ItFVKUV8l4M8DAABrSDBFAiEAlLJHnJ/rKFnnXuILqSnz4RMFEZ7dm7z8jb5pInq2TK4CIGGDoiYiiU74eRvQ+2AgaGiHiK962z5/j+5Cw4Z/cv6aQSECp0ENiqGxdtRwFQRq++9vfe5dy+MCDHTH9k/M0MWfBpb/////AgEAAAAAAAAA/WACdqkUJ1WJsYBzGalczxDV+4pQcLx9U42IrABjA29yZFEQYXBwbGljYXRpb24vanNvbgA6eyJsb2NhdG9yIjoic2VjOjEyMyIsInR5cGUiOiJzZWMiLCJzZWMiOnsiaXBmcyI6ImNpZHZhbCJ9fWhqIjFQdVFhN0s2Mk1pS0N0c3NTTEt5MWtoNTZXV1U3TXRVUjUDU0VUA2FwcAVvbGRldgR0eXBlA29yZAdzdWJUeXBlCmNvbGxlY3Rpb24EbmFtZQpvbGl2ZSB0ZXN0CnByZXZpZXdVcmwYaHR0cDovL2V4YW1wbGUuY29tLzEuam9nCXJveWFsdGllc0ylW3sidHlwZSI6InBheW1haWwiLCJkZXN0aW5hdGlvbiI6Impkb2VAaGFuZGNhc2guaW8iLCJwZXJjZW50YWdlIjoiMC4wMjUifSx7InR5cGUiOiJhZGRyZXNzIiwiZGVzdGluYXRpb24iOiIxTXZZaEZhakFSSjgyc2JneHVBWHppcTFGbWdTWTFYUXdEIiwicGVyY2VudGFnZSI6IjAuMDI1In1dC3N1YlR5cGVEYXRhP3sidHJhaXRzIjp7fSwiZGVzY3JpcHRpb24iOiJ0ZXN0IGNvbGxlY3Rpb24iLCJyYXJpdHlMYWJlbHMiOnt9fQF8BVNJR01BA0JTTSIxNGF5cGtzYlRNVXM2ZURGMU16NGh1a1I0VWhSdjM4VlFZQR9qiy/WPwq/RuoNMIwZQBPsskg3rQeFd8+RhylWkbt+FTVkkPnR/CLRRRZtQLspuiva729z7Tedox3GqQj0dIkBATAIAAAAAAAAABl2qRTfOVr//1msI87oQY5t9zc/sBJXnYisAAAAAA=="

func TestOliveTestRootSigmaValid(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(oliveTestRaw)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := transaction.NewTransactionFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := tx.TxID().String(); got != "acb61696ce28e81e66775707927c1c6f6771b2959a385032b2d493e3f7cfbf00" {
		t.Fatalf("txid %s", got)
	}
	sig := FirstValidSigma(tx, 0)
	if sig == nil {
		t.Fatal("expected valid SIGMA on olive test root")
	}
	if sig.SignerAddress != "14aypksbTMUs6eDF1Mz4hukR4UhRv38VQY" {
		t.Fatalf("signer %s", sig.SignerAddress)
	}
}
