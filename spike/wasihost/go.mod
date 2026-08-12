module github.com/dynatrace-oss/dtctl/spike/wasihost

go 1.26.4

require (
	github.com/dynatrace-oss/dtctl/sdk v0.0.0
	github.com/tetratelabs/wazero v1.9.0
	gopkg.in/yaml.v3 v3.0.1
)

require github.com/kr/text v0.2.0 // indirect

replace github.com/dynatrace-oss/dtctl/sdk => ../../sdk
