// Package peerv1 é o contrato gRPC entre células (RT-12, ADR-021), declarado à mão, sem
// .proto gerado (N-03): cinco métodos unários (Step, Forward, ReadBarrier, GateUpdate e
// Retained) e dois fluxos do servidor (CatchUp e CopyTable, RT-19), com um codec JSON
// forçado no cliente e no servidor. A resposta do GateUpdate leva o
// aplicado da follower (DC-15), um campo opcional: as versões anteriores continuam
// compatíveis nos dois sentidos.
package peerv1

import "encoding/json"

// CodecName é o nome do codec JSON do contrato.
const CodecName = "drjson"

// Codec serializa as mensagens do contrato com encoding/json.
type Codec struct{}

func (Codec) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (Codec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
func (Codec) Name() string                       { return CodecName }
