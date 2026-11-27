package extras

import (
	"fmt"

	"github.com/andreykaipov/goobs"
)

// OBSRecorder starts/stops OBS recording through obs-websocket (OBS: Tools > WebSocket Server Settings).
// It connects per call, so OBS can be started or restarted at any time.
type OBSRecorder struct {
	Host     string
	Port     int
	Password string
}

func (o *OBSRecorder) client() (*goobs.Client, error) {
	return goobs.New(fmt.Sprintf("%s:%d", o.Host, o.Port), goobs.WithPassword(o.Password))
}

func (o *OBSRecorder) Start() error {
	c, err := o.client()
	if err != nil {
		return err
	}
	defer c.Disconnect()
	_, err = c.Record.StartRecord()
	return err
}

func (o *OBSRecorder) Stop() (string, error) {
	c, err := o.client()
	if err != nil {
		return "", err
	}
	defer c.Disconnect()
	resp, err := c.Record.StopRecord()
	if err != nil {
		return "", err
	}
	return resp.OutputPath, nil
}
