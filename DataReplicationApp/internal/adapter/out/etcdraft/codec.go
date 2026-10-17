package etcdraft

import (
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog"
	"go.etcd.io/raft/v3/raftpb"

	"example.com/datareplication/internal/domain"
)

// toDomain converte uma entrada commitada do Raft. Entradas vazias da eleição e de
// ConfChange viram Noop, para os índices chegarem contíguos ao CommitPump e ao applier
// (RT-08). A Entry vai no payload em JSON.
func toDomain(e *raftpb.Entry) (domain.Entry, error) {
	idx, term := domain.Index(e.GetIndex()), domain.Term(e.GetTerm())
	if e.GetType() != raftpb.EntryNormal || len(e.GetData()) == 0 {
		return domain.Entry{Index: idx, Term: term, Kind: domain.KindNoop}, nil
	}
	var de domain.Entry
	if err := json.Unmarshal(e.GetData(), &de); err != nil {
		return domain.Entry{}, fmt.Errorf("etcdraft: entrada %d ilegível: %w", idx, err)
	}
	de.Index, de.Term = idx, term
	return de, nil
}

// kindOf traduz o tipo da mensagem do Raft para a regra de envio do domínio (sendrule).
func kindOf(m *raftpb.Message) domain.MsgKind {
	switch m.GetType() {
	case raftpb.MsgApp:
		return domain.MsgApp
	case raftpb.MsgAppResp:
		return domain.MsgAppResp
	case raftpb.MsgVote:
		return domain.MsgVote
	case raftpb.MsgVoteResp:
		return domain.MsgVoteResp
	case raftpb.MsgPreVote:
		return domain.MsgPreVote
	case raftpb.MsgPreVoteResp:
		return domain.MsgPreVoteResp
	case raftpb.MsgHeartbeat:
		return domain.MsgHeartbeat
	case raftpb.MsgHeartbeatResp:
		return domain.MsgHeartbeatResp
	case raftpb.MsgSnap:
		return domain.MsgSnap
	default:
		return domain.MsgOther
	}
}

// typeName é o rótulo `tipo` das métricas de mensagens do Raft (O-16), na mesma
// classificação de kindOf.
func typeName(m *raftpb.Message) string {
	switch kindOf(m) {
	case domain.MsgApp:
		return "app"
	case domain.MsgAppResp:
		return "app_resp"
	case domain.MsgVote:
		return "vote"
	case domain.MsgVoteResp:
		return "vote_resp"
	case domain.MsgPreVote:
		return "prevote"
	case domain.MsgPreVoteResp:
		return "prevote_resp"
	case domain.MsgHeartbeat:
		return "heartbeat"
	case domain.MsgHeartbeatResp:
		return "heartbeat_resp"
	case domain.MsgSnap:
		return "snap"
	default:
		return "outro"
	}
}

// raftLogger adapta o zerolog à interface de log do Raft.
type raftLogger struct{ l zerolog.Logger }

func (r raftLogger) Debug(v ...any)              {}
func (r raftLogger) Debugf(string, ...any)       {}
func (r raftLogger) Info(v ...any)               { r.l.Info().Msg(fmt.Sprint(v...)) }
func (r raftLogger) Infof(f string, v ...any)    { r.l.Info().Msgf(f, v...) }
func (r raftLogger) Warning(v ...any)            { r.l.Warn().Msg(fmt.Sprint(v...)) }
func (r raftLogger) Warningf(f string, v ...any) { r.l.Warn().Msgf(f, v...) }
func (r raftLogger) Error(v ...any)              { r.l.Error().Msg(fmt.Sprint(v...)) }
func (r raftLogger) Errorf(f string, v ...any)   { r.l.Error().Msgf(f, v...) }
func (r raftLogger) Fatal(v ...any)              { r.l.Fatal().Msg(fmt.Sprint(v...)) }
func (r raftLogger) Fatalf(f string, v ...any)   { r.l.Fatal().Msgf(f, v...) }
func (r raftLogger) Panic(v ...any)              { panic(fmt.Sprint(v...)) }
func (r raftLogger) Panicf(f string, v ...any)   { panic(fmt.Sprintf(f, v...)) }
