package obj

type SampleObject1 struct {
}

func (s *SampleObject1) Startup() error {
	// You can put your logic here when the service is starting up
	return nil
}

func (s *SampleObject1) Shutdown() error { return nil }

func (s *SampleObject1) Hello() string {
	return "Hello"
}
