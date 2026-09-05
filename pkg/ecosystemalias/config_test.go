package ecosystemalias

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/spf13/viper"
)

func TestConfigDefaultsAreDisabled(t *testing.T) {
	v := viper.New()
	var cfg Config
	cfg.SetDefaults(v, "ecosystemalias")

	if got := v.GetString("ecosystemalias.mode"); got != ModeDisabled {
		t.Fatalf("mode = %q, want %q", got, ModeDisabled)
	}
	if got := v.GetString("ecosystemalias.routes.prefix"); got != "/ecosystemalias" {
		t.Fatalf("routes.prefix = %q, want /ecosystemalias", got)
	}
	if !v.GetBool("ecosystemalias.routes.enabled") {
		t.Fatal("routes.enabled = false, want true")
	}
	if v.GetBool("ecosystemalias.sync.enabled") {
		t.Fatal("sync.enabled = true, want false")
	}
	if got := v.GetString("ecosystemalias.sync.queue_name"); got != QueueName {
		t.Fatalf("sync.queue_name = %q, want %q", got, QueueName)
	}
}

func TestConfigInitializeDisabled(t *testing.T) {
	svc, err := (&Config{Mode: ModeDisabled}).Initialize(t.Context(), nil, nil)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if svc != nil {
		t.Fatal("disabled module returned services")
	}
}

func TestConfigInitializeRequiresOverlayFactory(t *testing.T) {
	_, err := (&Config{Mode: ModeEmbedded}).Initialize(t.Context(), nil, nil)
	if err == nil {
		t.Fatal("Initialize succeeded without ModuleDeps")
	}

	want := errors.New("factory failed")
	_, err = (&Config{Mode: ModeEmbedded}).Initialize(t.Context(), nil, &overlay.ModuleDeps{
		Factory:     func(string) (overlaystorage.TopicStorage, error) { return nil, want },
		BeefStorage: testBeefStorage(),
	})
	if !errors.Is(err, want) {
		t.Fatalf("Initialize error = %v, want wrapped factory error", err)
	}
}

func TestConfigInitializeRequiresBeefStorage(t *testing.T) {
	factory, err := overlaystorage.NewSQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteFactory: %v", err)
	}
	t.Cleanup(func() { _ = factory.Close() })

	_, err = (&Config{Mode: ModeEmbedded}).Initialize(t.Context(), nil, &overlay.ModuleDeps{
		Factory:      factory.Factory(),
		TxTopicIndex: factory.TxTopicIndex(),
		RoutesConfig: &overlay.RoutesConfig{Enabled: true},
	})
	if err == nil || !strings.Contains(err.Error(), "BEEF storage") {
		t.Fatalf("Initialize error = %v, want missing BEEF storage", err)
	}
}

func TestConfigInitializeRegistersExactContractNames(t *testing.T) {
	factory, err := overlaystorage.NewSQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteFactory: %v", err)
	}
	t.Cleanup(func() { _ = factory.Close() })

	var requestedTopic string
	deps := &overlay.ModuleDeps{
		Factory: func(topic string) (overlaystorage.TopicStorage, error) {
			requestedTopic = topic
			return factory.Topic(topic)
		},
		TxTopicIndex: factory.TxTopicIndex(),
		BeefStorage:  testBeefStorage(),
		RoutesConfig: &overlay.RoutesConfig{Enabled: true},
	}
	cfg := &Config{Mode: ModeEmbedded, Routes: RoutesConfig{Enabled: true, Prefix: "/ecosystemalias"}}
	svc, err := cfg.Initialize(context.Background(), slog.Default(), deps)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if requestedTopic != TopicName {
		t.Fatalf("factory topic = %q, want %q", requestedTopic, TopicName)
	}
	managers := svc.Engine.ListTopicManagers()
	if len(managers) != 1 || managers[TopicName] == nil {
		t.Fatalf("topic managers = %#v, want only %q", managers, TopicName)
	}
	lookups := svc.Engine.ListLookupServiceProviders()
	if len(lookups) != 1 || lookups[LookupName] == nil {
		t.Fatalf("lookup services = %#v, want only %q", lookups, LookupName)
	}
	if svc.TopicManager.GetMetaData().Name != TopicName {
		t.Fatalf("topic metadata name = %q", svc.TopicManager.GetMetaData().Name)
	}
	if svc.Lookup.GetMetaData().Name != LookupName {
		t.Fatalf("lookup metadata name = %q", svc.Lookup.GetMetaData().Name)
	}
	if svc.OverlayRoutes == nil {
		t.Fatal("overlay routes were not initialized")
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestConfigInitializeCanDisableRoutes(t *testing.T) {
	factory, err := overlaystorage.NewSQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteFactory: %v", err)
	}
	t.Cleanup(func() { _ = factory.Close() })

	svc, err := (&Config{Mode: ModeEmbedded}).Initialize(t.Context(), nil, &overlay.ModuleDeps{
		Factory:      factory.Factory(),
		TxTopicIndex: factory.TxTopicIndex(),
		BeefStorage:  testBeefStorage(),
		RoutesConfig: &overlay.RoutesConfig{Enabled: true},
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if svc.OverlayRoutes != nil {
		t.Fatal("overlay routes initialized when module routes are disabled")
	}
}

func testBeefStorage() *beef.Storage {
	return beef.NewStorageFromProviders(nil, nil)
}

// A configured shared broadcaster must never leak into the no-send alias path.
type forbiddenAliasBroadcaster struct{}

func (*forbiddenAliasBroadcaster) Broadcast(*transaction.Transaction) (*transaction.BroadcastSuccess, *transaction.BroadcastFailure) {
	panic("alias admission must not broadcast")
}
func (*forbiddenAliasBroadcaster) BroadcastCtx(context.Context, *transaction.Transaction) (*transaction.BroadcastSuccess, *transaction.BroadcastFailure) {
	panic("alias admission must not broadcast")
}
func TestAliasAdmissionDoesNotInheritSharedPublicationEffects(t *testing.T) {
	factory, err := overlaystorage.NewSQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = factory.Close() })
	broadcaster := &forbiddenAliasBroadcaster{}
	bus := &overlay.P2PBus{}
	deps := &overlay.ModuleDeps{Factory: factory.Factory(), TxTopicIndex: factory.TxTopicIndex(), BeefStorage: testBeefStorage(), Broadcaster: broadcaster, P2PBus: bus}
	svc, err := (&Config{Mode: ModeEmbedded}).Initialize(t.Context(), nil, deps)
	if err != nil {
		t.Fatal(err)
	}
	if svc.Engine.Broadcaster != nil || svc.Engine.OnAdmission != nil {
		t.Fatal("alias module inherited pre-commit publication effects")
	}
	if deps.Broadcaster != broadcaster || deps.P2PBus != bus {
		t.Fatal("alias initialization mutated shared dependencies")
	}
}
