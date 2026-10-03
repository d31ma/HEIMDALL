package azurefunctions

import "github.com/d31ma/heimdall/internal/provider"

type ownedFunctions struct {
	reference provider.AppRef
	values    map[string]*nativeApp
	err       error
}

func (f ownedFunctions) read(apps []*nativeApp, reference provider.AppRef) (map[string]*nativeApp, error) {
	f.reference = reference
	f.values = map[string]*nativeApp{}
	f.collect(apps)
	return f.result()
}
func (f *ownedFunctions) collect(apps []*nativeApp) {
	for index := 0; index < len(apps) && f.err == nil; index++ {
		f.err = f.add(apps[index])
	}
}
func (f *ownedFunctions) add(app *nativeApp) error {
	owned := (resourceIdentity{}).owned(app, f.reference)
	return f.addOwned(app, owned)
}
func (f *ownedFunctions) addOwned(app *nativeApp, owned bool) error {
	if !owned {
		return nil
	}
	return f.record(app)
}
func (f *ownedFunctions) record(app *nativeApp) error {
	service := app.tagValue((resourceIdentity{}).serviceTag())
	actualName := (optionalValue[string]{}).read(app.Name)
	expectedName := (resourceIdentity{}).name(f.reference, service)
	err := f.requireUnique(f.values[service] != nil, actualName, expectedName)
	f.store(app, service, err)
	return err
}
func (ownedFunctions) requireUnique(duplicate bool, actual, expected string) error {
	if duplicate || actual != expected {
		return &AdapterError{Code: (functionContract{}).ownershipCode(), Message: "duplicate or mismatched managed function resource identity"}
	}
	return nil
}
func (f *ownedFunctions) store(app *nativeApp, service string, err error) {
	if err == nil {
		f.values[service] = app
	}
}
func (f ownedFunctions) result() (map[string]*nativeApp, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.values, nil
}
