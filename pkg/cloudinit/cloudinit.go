package cloudinit

import (
	"encoding/base64"

	"gopkg.in/yaml.v2"
)

func AddSSHUserToYaml(encodedUserData, user, group, sshkey string) (string, error) {
	cf := make(map[interface{}]interface{})
	yamlcontent, err := base64.StdEncoding.DecodeString(encodedUserData)
	if err != nil {
		return "", err
	}
	if err := yaml.Unmarshal(yamlcontent, &cf); err != nil {
		return "", err
	}

	// implements https://github.com/canonical/cloud-init/blob/master/cloudinit/config/cc_users_groups.py#L28-L71
	newUser := map[interface{}]interface{}{
		"name":        user,
		"sudo":        "ALL=(ALL) NOPASSWD:ALL",
		"lock_passwd": true,
		"groups":      group,

		// technically not in the spec, see this code for context
		// https://github.com/canonical/cloud-init/blob/master/cloudinit/distros/__init__.py#L394-L397
		"create_groups": false,

		"no_user_group": true,
		"ssh_authorized_keys": []string{
			sshkey,
		},
	}

	if val, ok := cf["users"]; ok {
		u := val.([]interface{})
		cf["users"] = append(u, newUser)
	} else {
		users := make([]interface{}, 1)
		users[0] = newUser
		cf["users"] = users
	}

	if val, ok := cf["groups"]; ok {
		g := val.([]interface{})
		var exists = false
		for _, v := range g {
			if y, _ := v.(string); y == group {
				exists = true
			}
		}
		if !exists {
			cf["groups"] = append(g, group)
		}
	} else {
		g := make([]interface{}, 1)
		g[0] = group
		cf["groups"] = g
	}

	yaml, err := yaml.Marshal(cf)
	if err != nil {
		return "", err
	}

	encodedOutputData := base64.StdEncoding.EncodeToString(yaml)
	return encodedOutputData, nil
}
