package eventlog

import (
	"github.com/dedis/protobuf"
	"github.com/dedis/student_18_omniledger/omniledger/darc"
	omniledger "github.com/dedis/student_18_omniledger/omniledger/service"

	"gopkg.in/dedis/cothority.v2"
	"gopkg.in/dedis/cothority.v2/skipchain"
	"gopkg.in/dedis/onet.v2"
)

// Client is a structure to communicate with the eventlog service
type Client struct {
	*onet.Client
	roster *onet.Roster
	// ID is the skipchain where events will be logged.
	ID skipchain.SkipBlockID
	// Signers are the Darc signers that will sign events sent with this client.
	Signers []*darc.Signer
	// Darc is the current Darc associated with this skipchain. Use it as a base
	// in case you need to evolve the permissions on the EventLog.
	Darc *darc.Darc
}

// NewClient creates a new client to talk to the eventlog service.
func NewClient(r *onet.Roster) *Client {
	return &Client{
		Client: onet.NewClient(cothority.Suite, ServiceName),
		roster: r,
	}
}

// Init initialises an event logging skipchain. A sucessful call
// updates the ID, Signer and Darc fields of the Client. The new
// skipchain has a Darc that requires one signature from owner.
func (c *Client) Init(owner *darc.Signer) error {
	rules1 := darc.InitRules([]*darc.Identity{owner.Identity()}, []*darc.Identity{})
	rules1["Spawn_eventlog"] = rules1.GetEvolutionExpr()

	d := darc.NewDarc(rules1, []byte("eventlog owner"))

	msg := &InitRequest{
		Owner:  *d,
		Roster: *c.roster,
	}
	reply := &InitResponse{}
	if err := c.SendProtobuf(c.roster.List[0], msg, reply); err != nil {
		return err
	}
	c.Darc = d
	c.Signers = []*darc.Signer{owner}
	c.ID = reply.ID
	return nil
}

// A LogID is an opaque unique identifier useful to find a given log message later.
type LogID []byte

// Log asks the service to log events.
func (c *Client) Log(ev ...Event) ([]LogID, error) {
	reply := &LogResponse{}
	tx, err := makeTx(ev, c.Darc.GetBaseID(), c.Signers)
	if err != nil {
		return nil, err
	}
	req := &LogRequest{
		SkipchainID: c.ID,
		Transaction: *tx,
	}
	if err := c.SendProtobuf(c.roster.List[0], req, reply); err != nil {
		return nil, err
	}
	out := make([]LogID, len(tx.Instructions))
	for i := range tx.Instructions {
		out[i] = tx.Instructions[i].ObjectID.Slice()
	}
	return out, nil
}

func makeTx(msgs []Event, darcID darc.ID, signers []*darc.Signer) (*omniledger.ClientTransaction, error) {
	// We need the identity part of the signatures before
	// calling ToDarcRequest() below, because the identities
	// go into the message digest.
	sigs := make([]darc.Signature, len(signers))
	for i, x := range signers {
		sigs[i].Signer = *(x.Identity())
	}

	instrNonce := omniledger.GenNonce()
	tx := omniledger.ClientTransaction{
		Instructions: make([]omniledger.Instruction, len(msgs)),
	}
	for i, msg := range msgs {
		eventBuf, err := protobuf.Encode(&msg)
		if err != nil {
			return nil, err
		}
		arg := omniledger.Argument{
			Name:  "event",
			Value: eventBuf,
		}
		tx.Instructions[i] = omniledger.Instruction{
			ObjectID: omniledger.ObjectID{
				DarcID:     darcID,
				InstanceID: omniledger.GenNonce(), // TODO figure out how to do the nonce property
			},
			Nonce:  instrNonce,
			Index:  i,
			Length: len(msgs),
			Spawn: &omniledger.Spawn{
				Args:       []omniledger.Argument{arg},
				ContractID: contractName,
			},
			Signatures: append([]darc.Signature{}, sigs...),
		}
	}
	for i := range tx.Instructions {
		darcSigs := make([]darc.Signature, len(signers))
		for j, signer := range signers {
			dr, err := tx.Instructions[i].ToDarcRequest()
			if err != nil {
				return nil, err
			}

			sig, err := signer.Sign(dr.Hash())
			if err != nil {
				return nil, err
			}
			darcSigs[j] = darc.Signature{
				Signature: sig,
				Signer:    *signer.Identity(),
			}
		}
		tx.Instructions[i].Signatures = darcSigs
	}
	return &tx, nil
}