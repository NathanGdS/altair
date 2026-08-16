npx autocannon -c 20 -d 5 -p 5 -m POST -H "Content-Type: application/json" -i "$(dirname "$0")/request.json" http://localhost:8080/publish
