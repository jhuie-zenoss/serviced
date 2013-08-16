package agent

import (
	"encoding/json"
	"fmt"
	serviced "github.com/zenoss/serviced"
	"testing"
)

const example_state = `
[{
    "ID": "2165c020b13cff5d7e675bedc8124fe6c561d384e1eb4896bfbbddb491ef1ccf",
    "Created": "2013-08-09T02:47:49.74930212-05:00",
    "Path": "/bin/sh",
    "Args": [
        "-c",
        "while true; do echo hello world; sleep 1; done"
    ],
    "Config": {
        "Hostname": "2165c020b13c",
        "User": "",
        "Memory": 0,
        "MemorySwap": 0,
        "CpuShares": 0,
        "AttachStdin": false,
        "AttachStdout": false,
        "AttachStderr": false,
        "PortSpecs": null,
        "Tty": false,
        "OpenStdin": false,
        "StdinOnce": false,
        "Env": null,
        "Cmd": [
            "/bin/sh",
            "-c",
            "while true; do echo hello world; sleep 1; done"
        ],
        "Dns": [
            "8.8.8.8",
            "8.8.4.4"
        ],
        "Image": "base",
        "Volumes": {},
        "VolumesFrom": "",
        "Entrypoint": [],
        "NetworkDisabled": false
    },
    "State": {
        "Running": true,
        "Pid": 4726,
        "ExitCode": 0,
        "StartedAt": "2013-08-09T02:47:49.75287917-05:00",
        "Ghost": false
    },
    "Image": "b750fe79269d2ec9a3c593ef05b4332b1d1a02a62b4accb2c21d589ff2f5f2dc",
    "NetworkSettings": {
        "IPAddress": "172.16.0.31",
        "IPPrefixLen": 16,
        "Gateway": "172.16.42.1",
        "Bridge": "docker0",
        "PortMapping": {
            "Tcp": {},
            "Udp": {}
        }
    },
    "SysInitPath": "/usr/bin/docker",
    "ResolvConfPath": "/var/lib/docker/containers/2165c020b13cff5d7e675bedc8124fe6c561d384e1eb4896bfbbddb491ef1ccf/resolv.conf",
    "Volumes": {},
    "VolumesRW": {}
}]
`

// Test parsing container state from docker.
func TestParseContainerState(t *testing.T) {
	var testState []serviced.ContainerState

	err := json.Unmarshal([]byte(example_state), &testState)
	if err != nil {
		t.Fatalf("Problem unmarshaling test state: ", err)
	}
	fmt.Printf("%s", testState)

}
