#!/bin/bash

bintype=$1
bin=bin/linux/$bintype

rm $bin
make $bin

docker build -f Dockerfile-${bintype#reboot-} -t jamiehannaford/demo-$bintype .
docker push jamiehannaford/demo-$bintype

resourcetype="daemonset"
if [ "$bintype" == "reboot-controller" ]; then
  resourcetype="deployment"
fi

kubectl --kubeconfig=./kubeconfig --namespace=reboot-operator delete $resourcetype $bintype
kubectl --kubeconfig=./kubeconfig create -f Examples/$bintype.yaml
