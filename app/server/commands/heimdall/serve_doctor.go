package main

type serveDoctor struct{}

func (serveDoctor) Run(deployment string) serveDoctorResult {
	output, failure := runSesame("doctor", "--deployment", deployment)
	return serveDoctorResult{output: output, failure: failure}
}
