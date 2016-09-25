// DO NOT EDIT: This file was auto-generated by "go generate"
// To regenerate run:
//   go install github.com/coreos/dex/cmd/genconfig
//   go generate <<fully qualified package name>>

package connector

import (
	"encoding/json"
	"errors"
	"fmt"
)

type NewConnectorConfigFunc func() ConnectorConfig

var (
	connectorTypes map[string]NewConnectorConfigFunc
)

func RegisterConnectorConfigType(connectorType string, fn NewConnectorConfigFunc) {
	if connectorTypes == nil {
		connectorTypes = make(map[string]NewConnectorConfigFunc)
	}

	if _, ok := connectorTypes[connectorType]; ok {
		panic(fmt.Sprintf("connector config type %q already registered", connectorType))
	}

	connectorTypes[connectorType] = fn
}

func NewConnectorConfigFromType(connectorType string) (ConnectorConfig, error) {
	fn, ok := connectorTypes[connectorType]
	if !ok {
		return nil, fmt.Errorf("unrecognized connector config type %q", connectorType)
	}

	return fn(), nil
}

func newConnectorConfigFromMap(m map[string]interface{}) (ConnectorConfig, error) {
	ityp, ok := m["type"]
	if !ok {
		return nil, errors.New("connector config type not set")
	}
	typ, ok := ityp.(string)
	if !ok {
		return nil, errors.New("connector config type not string")
	}
	cfg, err := NewConnectorConfigFromType(typ)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
