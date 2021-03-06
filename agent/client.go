// Copyright 2014 AdRoll, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package agent

import (
	"errors"
	"fmt"
	"time"

	"github.com/AdRoll/hologram/protocol"
	"github.com/AdRoll/hologram/transport/remote"
	"github.com/goamz/goamz/sts"
)

type CredentialsReceiver interface {
	SetCredentials(*sts.Credentials, string)
	SetClient(Client)
}

type Client interface {
	AssumeRole(role string) error
	GetUserCredentials() error
}

type client struct {
	connectionString string
	cr               CredentialsReceiver
}

func NewClient(connectionString string, cr CredentialsReceiver) *client {
	c := &client{
		connectionString: connectionString,
		cr:               cr,
	}
	if cr != nil {
		cr.SetClient(c)
	}
	return c
}

func (c *client) AssumeRole(role string) error {
	req := &protocol.ServerRequest{
		AssumeRole: &protocol.AssumeRole{
			Role: &role,
		},
	}

	return c.requestCredentials(req, role)
}

func (c *client) GetUserCredentials() error {
	req := &protocol.ServerRequest{
		GetUserCredentials: &protocol.GetUserCredentials{},
	}

	return c.requestCredentials(req, "")
}

func (c *client) requestCredentials(req *protocol.ServerRequest, role string) error {
	conn, err := remote.NewClient(c.connectionString)
	if err != nil {
		return err
	}

	msg := &protocol.Message{ServerRequest: req}

	err = conn.Write(msg)

	if err != nil {
		return err
	}

	for skip := 0; ; {
		msg, err = conn.Read()
		if err != nil {
			return err
		}
		if msg.GetServerResponse() != nil {
			serverResponse := msg.GetServerResponse()
			if serverResponse.GetChallenge() != nil {
				challenge := serverResponse.GetChallenge().GetChallenge()

				signature, err := SSHSign([]byte(challenge), skip)
				if err != nil {
					return err
				}
				if signature == nil {
					return errors.New("No keys worked")
				}

				msg = &protocol.Message{
					ServerRequest: &protocol.ServerRequest{
						ChallengeResponse: &protocol.SSHChallengeResponse{
							Signature: signature.Blob,
							Format:    &signature.Format,
						},
					},
				}

				err = conn.Write(msg)
				if err != nil {
					return err
				}
			} else if serverResponse.GetCredentials() != nil {
				credsResponse := serverResponse.GetCredentials()
				creds := &sts.Credentials{
					AccessKeyId:     credsResponse.GetAccessKeyId(),
					SessionToken:    credsResponse.GetAccessToken(),
					SecretAccessKey: credsResponse.GetSecretAccessKey(),
					Expiration:      time.Unix(credsResponse.GetExpiration(), 0),
				}
				c.cr.SetCredentials(creds, role)
				return nil
			} else if serverResponse.GetVerificationFailure() != nil {
				// try the next key
				skip++
			} else {
				return fmt.Errorf("unexpected message from server: %v", msg)
			}
		} else if msg.GetError() != "" {
			return errors.New(msg.GetError())
		} else {
			return fmt.Errorf("unexpected message from server: %v", msg)
		}
	}
}
