package lambda

type ownershipLabels struct{}

func (ownershipLabels) Manager() string        { return "dev.delma.heimdall.managed-by" }
func (ownershipLabels) ManagerValue() string   { return "heimdall" }
func (ownershipLabels) Project() string        { return "dev.delma.heimdall.project" }
func (ownershipLabels) App() string            { return "dev.delma.heimdall.app" }
func (ownershipLabels) Service() string        { return "dev.delma.heimdall.service" }
func (ownershipLabels) Revision() string       { return "dev.delma.heimdall.revision" }
func (ownershipLabels) ServiceHash() string    { return "dev.delma.heimdall.service-hash" }
func (ownershipLabels) SpecHash() string       { return "dev.delma.heimdall.spec-hash" }
func (ownershipLabels) Version() string        { return "dev.delma.heimdall.function-version" }
func (ownershipLabels) DefaultProject() string { return "default" }
func (ownershipLabels) Prefix() string         { return "hd-" }
func (ownershipLabels) Mixed() string          { return "mixed" }
