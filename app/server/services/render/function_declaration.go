package render

type functionDeclaration struct {
	RuntimeContract string `yaml:"runtime_contract"`
	Invocation      string `yaml:"invocation"`
	Timeout         string `yaml:"timeout"`
	Architecture    string `yaml:"architecture"`
}
