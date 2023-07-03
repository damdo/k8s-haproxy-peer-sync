#!/bin/bash

address=$1

if [ -z "$address" ]
then
  address="http://127.0.0.1:8080"
fi

tests=( ["333333"]="" ["figdfog"]="" ["ae23e3d"]="" ["dagfg"]="" )

echo "> Getting some urls keys assigned to a sticky backend.."
for k in "${!tests[@]}"
do
  v="$(curl -s "${address}"/a/a/"$k" | grep -E --only-matching "sticky-backend-[0-9]+")"
  tests["$k"]="$v"
  echo "For: $k | sticky backend: ${tests[$k]}"
done

echo "> Starting the tests.."
while true;
do
  n_tests="${#tests[@]}"
  successes=$n_tests
  for k in "${!tests[@]}"
  do
    actual="$(curl -s "${address}"/a/a/"$k" | grep -E --only-matching "sticky-backend-[0-9]+")"
    if [[ "$actual" == "" || "$actual" != "${tests[$k]}" ]]; then
      echo "WRONG: for $k | got: $actual exp: "${tests[$k]}""
      successes=$((successes-1))
    fi
  done
  echo "$successes/$n_tests tests succeded"
  sleep 3
done

