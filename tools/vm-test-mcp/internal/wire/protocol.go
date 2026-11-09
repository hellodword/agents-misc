package wire

import "slices"

// Protocol versions describe independently deployed clients and services.
const ProtocolVersion = 1

type Protocol struct {
	Version      int      `json:"version"`
	Capabilities []string `json:"capabilities"`
}

func CurrentProtocol() Protocol {
	return Protocol{ProtocolVersion, []string{"input_objects", "failure_evidence", "evidence_collection", "expected_exit_codes", "execution_recovery"}}
}

func RequireProtocol(protocol Protocol, capabilities ...string) error {
	if protocol.Version != ProtocolVersion {
		return Fail("protocol_mismatch", "Client and service require the same execution protocol version")
	}
	for _, capability := range capabilities {
		if !slices.Contains(protocol.Capabilities, capability) {
			return Fail("capability_unavailable", "Service does not advertise required capability: "+capability)
		}
	}
	return nil
}
