package main

import (
	"log"
	"tommynurwantoro/gontainer"
	"tommynurwantoro/gontainer/example/obj"
)

var appContainer gontainer.Container

func main() {
	// Register services
	appContainer.RegisterService("sampleObject1", new(obj.SampleObject1))

	// Start up services
	if err := appContainer.Ready(); err != nil {
		log.Panic("Failed to populate service", err)
	}

	// Get registered object from container
	obj1 := appContainer.GetServiceOrNil("sampleObject1").(*obj.SampleObject1)
	obj1.Hello()

	// Initialize objects with dependencies
	obj2 := &obj.SampleObject2{}
	obj2.Object.Hello()

	appContainer.Shutdown()
}
