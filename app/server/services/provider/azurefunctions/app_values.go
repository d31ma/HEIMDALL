package azurefunctions

type appValues struct{}

func (appValues) read(app *nativeApp) nativeApp {
	if app == nil {
		return nativeApp{}
	}
	return *app
}
func (appValues) properties(properties *nativeProperties) nativeProperties {
	if properties == nil {
		return nativeProperties{}
	}
	return *properties
}
