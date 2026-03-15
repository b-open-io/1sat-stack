package p2p

import (
	"testing"
)

func TestEnvelope_RoundTrip(t *testing.T) {
	env := &Envelope{
		Version: 0,
		Method:  MethodRequestNode,
		Status:  StatusRequest,
		Topic:   "tm_bap",
		Price:   0,
		Payment: nil,
		Body:    []byte{0x01, 0x02, 0x03},
	}

	data := env.Serialize()
	got, err := DeserializeEnvelope(data)
	if err != nil {
		t.Fatal(err)
	}

	if got.Version != env.Version {
		t.Errorf("version: want %d, got %d", env.Version, got.Version)
	}
	if got.Method != env.Method {
		t.Errorf("method: want %d, got %d", env.Method, got.Method)
	}
	if got.Status != env.Status {
		t.Errorf("status: want %d, got %d", env.Status, got.Status)
	}
	if got.Topic != env.Topic {
		t.Errorf("topic: want %q, got %q", env.Topic, got.Topic)
	}
	if got.Price != env.Price {
		t.Errorf("price: want %d, got %d", env.Price, got.Price)
	}
	if len(got.Payment) != 0 {
		t.Errorf("payment: want empty, got %d bytes", len(got.Payment))
	}
	assertBytes(t, "body", env.Body, got.Body)
}

func TestEnvelope_EmptyFields(t *testing.T) {
	env := &Envelope{
		Version: 0,
		Method:  MethodInitialRequest,
		Status:  StatusRequest,
	}

	data := env.Serialize()
	got, err := DeserializeEnvelope(data)
	if err != nil {
		t.Fatal(err)
	}

	if got.Topic != "" {
		t.Errorf("topic: want empty, got %q", got.Topic)
	}
	if len(got.Payment) != 0 {
		t.Errorf("payment: want empty, got %d bytes", len(got.Payment))
	}
	if len(got.Body) != 0 {
		t.Errorf("body: want empty, got %d bytes", len(got.Body))
	}
}

func TestEnvelope_PaymentRequired(t *testing.T) {
	env := &Envelope{
		Version: 0,
		Method:  MethodNode,
		Status:  StatusPaymentRequired,
		Topic:   "tm_bsv21_abc123",
		Price:   50,
	}

	data := env.Serialize()
	got, err := DeserializeEnvelope(data)
	if err != nil {
		t.Fatal(err)
	}

	if got.Status != StatusPaymentRequired {
		t.Errorf("status: want %d, got %d", StatusPaymentRequired, got.Status)
	}
	if got.Price != 50 {
		t.Errorf("price: want 50, got %d", got.Price)
	}
}

func TestEnvelope_WithPayment(t *testing.T) {
	payment := make([]byte, 250)
	for i := range payment {
		payment[i] = byte(i)
	}

	env := &Envelope{
		Version: 0,
		Method:  MethodRequestNode,
		Status:  StatusRequest,
		Topic:   "tm_bap",
		Payment: payment,
		Body:    []byte{0xAA, 0xBB},
	}

	data := env.Serialize()
	got, err := DeserializeEnvelope(data)
	if err != nil {
		t.Fatal(err)
	}

	assertBytes(t, "payment", payment, got.Payment)
	assertBytes(t, "body", env.Body, got.Body)
}

func TestEnvelope_ErrorStatus(t *testing.T) {
	errMsg := []byte("output not found")
	env := &Envelope{
		Version: 0,
		Method:  MethodNode,
		Status:  StatusNotFound,
		Topic:   "tm_bap",
		Body:    errMsg,
	}

	data := env.Serialize()
	got, err := DeserializeEnvelope(data)
	if err != nil {
		t.Fatal(err)
	}

	if got.Status != StatusNotFound {
		t.Errorf("status: want %d, got %d", StatusNotFound, got.Status)
	}
	assertBytes(t, "body", errMsg, got.Body)
}

func TestEnvelope_TooShort(t *testing.T) {
	_, err := DeserializeEnvelope([]byte{0x00, 0x01})
	if err == nil {
		t.Fatal("expected error for short input")
	}
}

func assertBytes(t *testing.T, name string, want, got []byte) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s: length mismatch: want %d, got %d", name, len(want), len(got))
	}
	for i := range want {
		if want[i] != got[i] {
			t.Errorf("%s[%d]: want %x, got %x", name, i, want[i], got[i])
			return
		}
	}
}
