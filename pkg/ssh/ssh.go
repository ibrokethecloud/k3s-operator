package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	b64 "encoding/base64"
	"errors"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/tmc/scp"
	gossh "golang.org/x/crypto/ssh"
)

var (
	ErrKeyGeneration = errors.New("Unable to generate key")
	ErrValidation    = errors.New("Unable to validate key")
	ErrPublicKey     = errors.New("Unable to convert public key")
)

type KeyPair struct {
	PrivateKey []byte
	PublicKey  []byte
}

type RemoteConnection struct {
	Address string
	Config  gossh.ClientConfig
	User    string
}

func NewKeyPair() (keyPair *KeyPair, err error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, ErrKeyGeneration
	}

	if err := priv.Validate(); err != nil {
		return nil, ErrValidation
	}

	privDer := x509.MarshalPKCS1PrivateKey(priv)

	pubSSH, err := gossh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, ErrPublicKey
	}

	return &KeyPair{
		PrivateKey: privDer,
		PublicKey:  gossh.MarshalAuthorizedKey(pubSSH),
	}, nil
}

// NewRemoteConnection generates a new remoteconnection struct which can be used to generate
// a new ssh connection used for subsequent provisioning and management
func NewRemoteConnection(address string, user string, privateKey string) (remoteConnection RemoteConnection,
	err error) {
	decodedPrivateKey, err := b64.StdEncoding.DecodeString(privateKey)
	if err != nil {
		return remoteConnection, err
	}
	parsedPrivateKey, err := gossh.ParsePrivateKey(decodedPrivateKey)
	if err != nil {
		return remoteConnection, err
	}

	var authInfo []gossh.AuthMethod
	authInfo = append(authInfo, gossh.PublicKeys(parsedPrivateKey))
	sshConfig := gossh.ClientConfig{
		User:            user,
		Auth:            authInfo,
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	}
	remoteConnection = RemoteConnection{
		Address: address,
		User:    user,
		Config:  sshConfig,
	}

	return remoteConnection, nil
}

// CheckConnection is used to ensure that controller can ssh to remote instance
func (r *RemoteConnection) CheckConnection() (client *gossh.Client, status bool) {
	client, err := gossh.Dial("tcp", r.Address, &r.Config)
	if err == nil {
		status = true
	}

	return client, status
}

//Remote is used to execute a remote command on an instance
func (r *RemoteConnection) Remote(command string) (err error) {
	client, ok := r.CheckConnection()
	if !ok {
		return fmt.Errorf("Error during connection check")
	}
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	err = session.Run(command)
	return err
}

//RemoteFile is used to copy the contents to a remote file
func (r *RemoteConnection) RemoteFile(filePath string, content string) (err error) {
	tmpFile, err := ioutil.TempFile("/tmp", "content")
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintln(tmpFile, content); err != nil {
		return err
	}

	if err = tmpFile.Close(); err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())

	client, ok := r.CheckConnection()
	if !ok {
		return fmt.Errorf("Error during filecopy connection check")
	}

	session, err := client.NewSession()
	if err != nil {
		return err
	}

	err = scp.CopyPath(tmpFile.Name(), filePath, session)
	return err
}
