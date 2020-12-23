package template

import (
	"bytes"
	"text/template"
)

// Command generation for the helper script
type Command struct {
	BootStep   string
	ExtraSteps string
	NodeName   string
}

const HelperScript = `
#!/bin/sh
{{ if eq .Command.BootStep "" }}curl -sfL https://get.k3s.io | sh - {{else}}{{ .Command.BootStep }}{{ end }}
## Extra arguments can be set here
{{ .Command.ExtraSteps }}
NODE='{{- .Command.NodeName }}'
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
func GenerateCommand(bootStep string, extraStep string, nodeName string) (output bytes.Buffer, err error) {
	c := Command{
		BootStep:   bootStep,
		ExtraSteps: extraStep,
		NodeName:   nodeName,
	}

	wrapperTemplate := template.Must(template.New("HelperScript").Parse(HelperScript))
	err = wrapperTemplate.Execute(&output, c)
	return output, err
}
