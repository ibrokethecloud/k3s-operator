package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	b64 "encoding/base64"
	"encoding/pem"
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

	encodedPrvKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Headers: nil, Bytes: privDer})

	return &KeyPair{
		PrivateKey: encodedPrvKey,
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
func (r *RemoteConnection) CheckConnection() (client *gossh.Client, err error) {
	client, err = gossh.Dial("tcp", r.Address, &r.Config)
	/*if err == nil {
		status = true
	}*/

	return client, err
}

//Remote is used to execute a remote command on an instance
func (r *RemoteConnection) Remote(command string) (output []byte, err error) {
	client, err := r.CheckConnection()
	if err != nil {
		return output, err
	}
	session, err := client.NewSession()
	if err != nil {
		return output, err
	}
	defer session.Close()

	output, err = session.Output(command)
	return output, err
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

	client, err := r.CheckConnection()
	if err != nil {
		return err
	}

	session, err := client.NewSession()
	if err != nil {
		return err
	}

	err = scp.CopyPath(tmpFile.Name(), filePath, session)
	return err
}
