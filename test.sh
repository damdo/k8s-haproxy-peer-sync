#!/bin/bash

declare -A tests=( ["333333"]="" ["figdfog"]="" ["ae23e3d"]="" ["dagfg"]="" )

echo "> Getting some urls keys assigned to a server.."
for k in "${!tests[@]}"
do
  v="$(curl -s http://127.0.0.1:80/a/a/"$k" | grep -E --only-matching "sticky-backend-[0-9]+")"
  tests["$k"]="$v"
  echo "For: $k | server: ${tests[$k]}"
done

echo "> Starting the tests.. Will only log if anything is wrong"
while true;
do
  for k in "${!tests[@]}"
  do
    actual="$(curl -s http://127.0.0.1:80/a/a/"$k" | grep -E --only-matching "sticky-backend-[0-9]+")"
    if [[ "$actual" != "" && "$actual" != "${tests[$k]}" ]]; then
      echo "WRONG: for $k | got: $actual exp: ${tests[$k]}"
    fi
  done
  sleep 3
done

