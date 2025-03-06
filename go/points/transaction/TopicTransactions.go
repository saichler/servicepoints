package transaction

import (
	"github.com/saichler/layer8/go/overlay/protocol"
	"github.com/saichler/shared/go/share/interfaces"
	"github.com/saichler/shared/go/types"
	"strings"
	"sync"
	"time"
)

type TopicTransactions struct {
	mtx        *sync.Mutex
	pendingMap map[string]*types.Message
	locked     *types.Message
}

func newTopicTransactions() *TopicTransactions {
	tt := &TopicTransactions{}
	tt.pendingMap = make(map[string]*types.Message)
	tt.mtx = &sync.Mutex{}
	return tt
}

func createTransaction(msg *types.Message) {
	if msg.Tr == nil {
		msg.Tr = &types.Transaction{}
		msg.Tr.Id = interfaces.NewUuid()
		msg.Tr.StartTime = time.Now().Unix()
		msg.Tr.State = types.TransactionState_Create
	}
}

func (this *TopicTransactions) addTransaction(msg *types.Message) {
	this.mtx.Lock()
	defer this.mtx.Unlock()
	_, ok := this.pendingMap[msg.Tr.Id]
	if ok {
		panic("Trying to add a duplicate transaction")
	}
	msg.Tr.State = types.TransactionState_Create
	this.pendingMap[msg.Tr.Id] = msg
}

func (this *TopicTransactions) commited(msg *types.Message, lock bool) {
	if lock {
		this.mtx.Lock()
		defer this.mtx.Unlock()
	}
	if this.locked == nil {
		return
	}
	if this.locked.Tr.Id == msg.Tr.Id {
		this.locked = nil
	}
}

func (this *TopicTransactions) commit(msg *types.Message, vnic interfaces.IVirtualNetworkInterface, lock bool) bool {
	if lock {
		this.mtx.Lock()
		defer this.mtx.Unlock()
	}

	if msg.Tr.State != types.TransactionState_Commit {
		panic("commit: Unexpected transaction state " + msg.Tr.State.String())
	}

	if this.locked == nil {
		msg.Tr.State = types.TransactionState_Errored
		msg.Tr.Error = "Commit: No pending transaction"
		return false
	}

	if this.locked.Tr.Id != msg.Tr.Id {
		msg.Tr.State = types.TransactionState_Errored
		msg.Tr.Error = "Commit: commit is for another transaction"
		return false
	}

	if this.locked.Tr.State != types.TransactionState_Locked &&
		this.locked.Tr.State != types.TransactionState_Commit { //The state will be commit if the message hit the leader
		msg.Tr.Error = "Commit: Transaction is not in locked state " + msg.Tr.State.String()
		msg.Tr.State = types.TransactionState_Errored
		return false
	}

	if time.Now().Unix()-this.locked.Tr.StartTime >= 2 { //@TODO add the timeout
		msg.Tr.State = types.TransactionState_Errored
		msg.Tr.Error = "Commit: Transaction has timed out"
		return false
	}

	servicePoints := vnic.Resources().ServicePoints()
	if msg.Action == types.Action_Notify {
		//_, err := servicePoints.Notify()
	} else {
		pb, err := protocol.ProtoOf(this.locked, vnic.Resources())
		if err != nil {
			msg.Tr.State = types.TransactionState_Errored
			msg.Tr.Error = "Commit: Protocol Error: " + err.Error()
			return false
		}
		_, err = servicePoints.Handle(pb, this.locked.Action, vnic, this.locked, true)
		if err != nil {
			msg.Tr.State = types.TransactionState_Errored
			msg.Tr.Error = "Commit: Handle Error: " + err.Error()
			return false
		}
	}

	msg.Tr.State = types.TransactionState_Commited
	return true
}

func (this *TopicTransactions) lock(msg *types.Message, lock bool) bool {
	if lock {
		this.mtx.Lock()
		defer this.mtx.Unlock()
	}

	if msg.Tr.State != types.TransactionState_Lock {
		panic("lock: Unexpected transaction state " + msg.Tr.State.String())
	}

	if this.locked == nil {
		m := this.pendingMap[msg.Tr.Id]
		if m == nil {
			panic("Can't find message " + msg.Tr.Id)
		}
		this.locked = m
		msg.Tr.State = types.TransactionState_Locked
		m.Tr.State = msg.Tr.State
		return true
	} else if this.locked.Tr.Id != msg.Tr.Id &&
		this.locked.Tr.State != types.TransactionState_Locked &&
		strings.Compare(this.locked.Tr.Id, msg.Tr.Id) == -1 {
		m := this.pendingMap[msg.Tr.Id]
		if m == nil {
			panic("Can't find message " + msg.Tr.Id)
		}
		this.locked = m
		msg.Tr.State = types.TransactionState_Locked
		m.Tr.State = msg.Tr.State
		return true
	}

	msg.Tr.State = types.TransactionState_LockFailed
	return false
}
