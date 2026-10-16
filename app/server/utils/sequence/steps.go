package sequence

type Steps struct{ Actions []func() error }

func (s Steps) Run() error { return s.run() }
func (s Steps) run() error {
	var err error
	for index := 0; index < len(s.Actions) && err == nil; index++ {
		err = s.Actions[index]()
	}
	return err
}
