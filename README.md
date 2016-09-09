Polymer
====

A TCP server that receives logs in `json` format and write them to the file.

### Log Format
```json
{
  "Log": "the data of current log",
  "FileName": "the file name current log wrote to"
}
```

### Build
```
# build polymer-server
make

# how to run polymer-server?
./polymer-server -h
```

### Test
echo `'{"Log":"0123456789","FileName":"polymer_0"}'` | nc 127.0.0.1 5565
