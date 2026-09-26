package desktop

import (
	"context"
	"testing"
)

func TestDesktopSetupDisconnectRetiresReferencesAndReAdmits(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	connections := 0
	m := newManager(func(context.Context) (driverClient, error) { connections++; return f, nil })
	defer m.Close()
	r, err := m.Bind(t.Context(), RunOptions{Mode: BackgroundOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	o := observed(t, r, false)
	if err := m.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err := r.Act(t.Context(), ActRequest{Kind: "click", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token})
	if err == nil || result.Dispatched || f.count("click") != 0 {
		t.Fatal("retired reference executed")
	}
	if m.Status().Connected || f.closed != 1 {
		t.Fatal("old connection retained")
	}
	_ = observed(t, r, false)
	if connections != 2 || m.Status().Generation != 2 {
		t.Fatal("next observation did not re-admit")
	}
}
