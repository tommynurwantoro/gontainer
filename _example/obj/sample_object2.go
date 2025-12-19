package obj

type SampleObject2 struct {
	Object SampleObject1 `inject:"sampleObject1"`
}

func (s *SampleObject2) Startup() error {
	// You can put your logic here when the service is starting up
	return nil
}

func (s *SampleObject2) Shutdown() error { return nil }
