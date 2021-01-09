package template

import (
	"bytes"
	"text/template"
)

// Command generation for the helper script
type Command struct {
	BootURL    string
	ExtraSteps string
	NodeName   string
	Role       string
	Version    string
	Channel    string
}

const HelperScript = `
#!/bin/sh
{{ if eq .BootURL "" }}curl -sfL https://get.k3s.io {{else}}{{ .BootURL }}{{ end }} | INSTALL_K3S_VERSION="{{- .Version}}" INSTALL_K3S_CHANNEL="{{- .Channel }}" sh -s - {{.Role}} 
## Extra arguments can be set here
{{ .ExtraSteps }}
NODE='{{- .NodeName }}'
## Standard check ##
sleep 60
CURRENT_NODE_STATUS=$(kubectl get nodes | grep ${NODE} | awk '{print tolower($2)}')
while [[ ${CURRENT_NODE_STATUS} != "ready" ]]
do
    sleep 60
    CURRENT_NODE_STATUS=$(kubectl get nodes | grep ${NODE} | awk '{print tolower($2)}')
done
`

// Helper function to generate the script based on contents of config Map
func GenerateCommand(bootURL string, extraStep string, nodeName string, role string,
	version string, channel string) (output bytes.Buffer, err error) {
	c := Command{
		BootURL:    bootURL,
		ExtraSteps: extraStep,
		NodeName:   nodeName,
		Role:       role,
		Version:    version,
		Channel:    channel,
	}

	wrapperTemplate := template.Must(template.New("HelperScript").Parse(HelperScript))
	err = wrapperTemplate.Execute(&output, c)
	return output, err
}
