package messages

import (
	"fmt"
	"io"
	"reflect"

	"github.com/rootless-containers/rootlesskit/v2/pkg/lowlevelmsgutil"
	"github.com/sirupsen/logrus"
)

// Message for parent-child communication.
// Sent as JSON, with uint32le length header.
type Message struct {
	Name string // Name is like "MessageParentHello". Automatically filled on [Send.
	U
}

func (m *Message) FulfillName() error {
	uT := reflect.TypeOf(m.U)
	uV := reflect.ValueOf(m.U)
	uN := uT.NumField()
	for i := 0; i < uN; i++ {
		uTF := uT.Field(i)
		uVF := uV.Field(i)
		if !uVF.IsNil() {
			m.Name = uTF.Name
			return nil
		}
	}
	return fmt.Errorf("failed to fulfill the name for message %+v", m)
}

// U is a union.
type U struct {
	*ParentHello
	*ChildHello
	*ParentInitIdmapCompleted
	*ChildInitUserNSCompleted
	*ParentInitNetworkDriverCompleted
	*ParentInitPortDriverCompleted
}

type ParentHello struct {
}

type ChildHello struct {
}

type ParentInitIdmapCompleted struct {
}

type ChildInitUserNSCompleted struct {
}

type ParentInitNetworkDriverCompleted struct {
	// Fields are empty for HostNetwork.
	Network interface{}
	Dev     string
	IP      string
	Netmask int
	Gateway string
	DNS     []string
	MTU     int
	// NetworkDriverOpaque strings are specific to driver
	NetworkDriverOpaque map[string]string
}

type ParentInitPortDriverCompleted struct {
	// Fields are empty for port driver "none"
	PortDriverOpaque map[string]string
}

func Send(w io.Writer, m *Message) error {
	if m.Name == "" {
		if err := m.FulfillName(); err != nil {
			return err
		}
	}
	logrus.Debugf("Sending %+v", m)
	if _, err := lowlevelmsgutil.MarshalToWriter(w, m); err != nil {
		return fmt.Errorf("failed to send message %+v: %w", m, err)
	}
	return nil
}

func Recv(r io.Reader) (*Message, error) {
	var m Message
	if _, err := lowlevelmsgutil.UnmarshalFromReader(r, &m); err != nil {
		return nil, err
	}
	logrus.Debugf("Received %+v", m)
	if m.Name == "" {
		return nil, fmt.Errorf("failed to parse message %+v", m)
	}
	return &m, nil
}

func WaitFor(r io.Reader, name string) (*Message, error) {
	msg, err := Recv(r)
	if err != nil {
		return nil, err
	}
	if msg.Name != name {
		return nil, fmt.Errorf("expected %q, got %+v", name, msg)
	}
	return msg, nil
}

func Name(x interface{}) string {
	t := reflect.TypeOf(x)
	if t.Kind() == reflect.Ptr {
		return t.Elem().Name()
	}
	return t.Name()
}
