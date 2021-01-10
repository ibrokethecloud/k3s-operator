# K3s Operator: Operator to launch k3s clusters

The operator can be used to launch k3s clusters including compute for those clusters.

At present the operator supports provisioning clusters on two compute types

* Custom: Compute has already been provisioned by the user
* AWS: ec2 instances are provisioned in the background using the [ec2-operator](https://github.com/ibrokethecloud/ec2-operator)

## Sample Custom Spec:

```bigquery
apiVersion: k3s.io/v1alpha1
kind: Cluster
metadata:
  name: custom
spec:
  # Add fields here
  channel: "stable"
  version: "v1.19.5+k3s2"
  nodePools:
  - name: master-1
    count: 1
    role: server
    user: ubuntu
    sshPrivateKey: "base64-encoded-key"
    instanceSpec:
      custom:
        address: "172.16.132.163"
  - name: master-2
    count: 1
    role: server
    user: ubuntu
    sshPrivateKey: "base64-encoded-key"
    instanceSpec:
      custom:
        address: "172.16.133.142"        
  - name: master-3
    count: 1
    role: server
    user: ubuntu
    sshPrivateKey: "base64-encoded-key"
    instanceSpec:
      custom:
        address: "172.16.134.235"             
```

In this example the operator will launch a HA k3s cluster with the 3 nodes on the existing 3 nodes.

Please make sure to specify a `sshPrivateKey` to allow the operator to ssh into the instances to perform k3s provisioning.

Once cluster is ready KubeConfig can be extracted from the cluster status.

```bigquery
▶ kubectl get cluster.k3s.io
NAME     STATUS    MESSAGE
aws      Running
custom   Running

▶ kubectl get cluster.k3s.io custom -o json | jq -r .status.kubeConfig | base64 -d > /tmp/kubeconfig

▶ kubectl get nodes --kubeconfig=/tmp/kubeconfig
NAME             STATUS   ROLES         AGE   VERSION
172.16.132.163   Ready    etcd,master   19h   v1.19.5+k3s2
172.16.133.142   Ready    etcd,master   19h   v1.19.5+k3s2
172.16.134.235   Ready    etcd,master   19h   v1.19.5+k3s2
```


## Sample AWS Spec:
```bigquery
apiVersion: k3s.io/v1alpha1
kind: Cluster
metadata:
  name: aws
spec:
  # Add fields here
  channel: "stable"
  version: "v1.19.5+k3s2"
  nodePools:
  - name: master
    count: 3
    role: server
    user: k3s
    group: k3s
    instanceSpec:
      aws:
        credentialSecret: aws-secret
        imageID: ami-00f9943076a0e3364
        subnetID: subnet-4e1db116
        region: ap-southeast-2
        securityGroupIDS:
          - sg-0b5537df034ae6860
        publicIPAddress: true
        instanceType: t2.medium
        userData: additional base64 encoded userdata
  - name: agent
    count: 3
    role: agent
    user: k3s
    group: k3s
    instanceSpec:
      aws:
        credentialSecret: aws-secret
        imageID: ami-00f9943076a0e3364
        subnetID: subnet-4e1db116
        region: ap-southeast-2
        securityGroupIDS:
          - sg-0b5537df034ae6860
        publicIPAddress: true
        instanceType: t2.medium      
        userData: additional base64 encoded userdata
```

In this case the operator will launch 3 instances for the master nodes and 3 for the agent nodes.

If no sshPrivateKey is specified, one will be dynamically generated and injected via cloud-init in the aws instances.

As usual once cluster is running kubeconfig can be extracted from the status.

```bigquery
▶ kubectl get cluster.k3s.io
NAME     STATUS    MESSAGE
aws      Running
custom   Running

▶  kubectl get cluster.k3s.io aws  -o json | jq -r .status.kubeConfig | base64 -d > /tmp/kubeconfig

▶ kubectl get nodes --kubeconfig=/tmp/kubeconfig
NAME             STATUS   ROLES         AGE   VERSION
13.211.197.104   Ready    etcd,master   36m   v1.19.5+k3s2
13.238.217.238   Ready    <none>        28m   v1.19.5+k3s2
3.106.132.107    Ready    etcd,master   32m   v1.19.5+k3s2
3.26.37.77       Ready    etcd,master   35m   v1.19.5+k3s2
3.26.8.181       Ready    <none>        31m   v1.19.5+k3s2
54.252.166.115   Ready    <none>        29m   v1.19.5+k3s2
```

As part of the provisioning, the user / group specified in the spec will be created via cloud-init on the launched computed 
and k3s provisioning will be performed by the same. 

If any userData is specified in the instanceTemplate spec, the same will be merged with the operator generated userdata.


### K3s version Updates
Updating the version field on the cluster spec will trigged an in place upgrade for the cluster.

```bigquery
apiVersion: k3s.io/v1alpha1
kind: Cluster
metadata:
  name: custom
spec:
  # Add fields here
  channel: "stable"
  version: "v1.19.3+k3s2" #change to v1.20.0+k3s2 to trigger upgrade
```