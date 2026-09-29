package desktop

type Application struct {
	Ref      string `json:"app_ref,omitempty"`
	Name     string `json:"name"`
	BundleID string `json:"bundle_id,omitempty"`
	PID      int    `json:"pid,omitempty"`
	Running  bool   `json:"running"`
}

type appLaunchTarget struct {
	bundleID, path string
}
