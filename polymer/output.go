package polymer

import (
	"container/list"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const RELOAD string = "reload"

type cacheEntry struct {
	key  string
	data []byte
	time int64
}

type outBatch struct {
	path string
	data []byte
}

type msgPack struct {
	Log      string `json:"Log"`
	FileName string `json:"FileName"`
}

type LRU struct {
	sync.Mutex

	// MaxEntries is the maximum number of cache entries before
	// an item is evicted. Zero means no limit.
	MaxEntries int

	// OnEvicted optionally specificies a callback function to be
	// executed when an entry is purged from the cache.
	OnEvicted func(value *cacheEntry)

	list  *list.List
	cache map[string]*list.Element
}

// New creates a new LRU.
// If maxEntries is zero, the cache has no limit and it's assumed
// that eviction is done by the caller.
func NewLRU(maxEntries int) *LRU {
	return &LRU{
		MaxEntries: maxEntries,
		list:       list.New(),
		cache:      make(map[string]*list.Element),
	}
}

// Add adds a value to the cache.
func (lru *LRU) Add(batch outBatch) {
	if lru.cache == nil {
		lru.cache = make(map[string]*list.Element)
		lru.list = list.New()
	}

	var (
		ok  bool
		ele *list.Element
	)

	key := batch.path
	if ele, ok = lru.cache[key]; ok {
		lru.list.MoveToFront(ele)
		ce := ele.Value.(*cacheEntry)
		ce.data = append(ce.data, batch.data...)
		return
	}

	data := make([]byte, len(batch.data))
	copy(data, batch.data)
	value := cacheEntry{
		key:  batch.path,
		data: data,
		time: time.Now().Unix(),
	}
	ele = lru.list.PushFront(&value)
	lru.cache[key] = ele
	if lru.MaxEntries != 0 && lru.Len() > lru.MaxEntries {
		lru.RemoveOldest()
	}
}

// Remove removes the provided key from the cache.
func (lru *LRU) Remove(key string) {
	if lru.cache == nil {
		return
	}
	if ele, hit := lru.cache[key]; hit {
		lru.removeElement(ele)
	}
}

// RemoveOldest removes the oldest item from the cache.
func (lru *LRU) RemoveOldest() {
	if lru.cache == nil {
		return
	}
	ele := lru.list.Back()
	if ele != nil {
		lru.removeElement(ele)
	}
}

func (lru *LRU) removeElement(e *list.Element) {
	lru.list.Remove(e)
	ce := e.Value.(*cacheEntry)
	delete(lru.cache, ce.key)
	if lru.OnEvicted != nil {
		lru.OnEvicted(ce)
	}
}

// Len returns the number of items in the cache.
func (lru *LRU) Len() int {
	if lru.cache == nil {
		return 0
	}
	return lru.list.Len()
}

// Output plugin that writes message contents to a file on the file system.
type FileOutput struct {
	*FileOutputConfig
	lru         *LRU
	folder      string
	perm        os.FileMode
	recvChan    <-chan string
	folderPerm  os.FileMode
	flushTicker *time.Ticker
}

// ConfigStruct for FileOutput plugin.
type FileOutputConfig struct {
	// File output folder.
	Folder string

	// Output file permissions (default "644").
	Perm string

	// Interval at which accumulated file data should be written to disk, in
	// milliseconds (default 1000, i.e. 1 second). Set to 0 to disable.
	FlushInterval uint32

	// Number of messages to accumulate until file data should be written to
	// disk (default 1, minimum 1).
	FlushCount uint32

	// Permissions to apply to directories created for FileOutput's parent
	// directory if it doesn't exist.  Must be a string representation of an
	// octal integer. Defaults to "700".
	FolderPerm string
}

func NewFileOutput(folder string, recvChan <-chan string) (*FileOutput, error) {
	fo := &FileOutput{
		folder:   folder,
		recvChan: recvChan,
	}

	foc := fo.ConfigStruct()
	err := fo.Init(foc)

	return fo, err
}

func (o *FileOutput) ConfigStruct() interface{} {
	return &FileOutputConfig{
		Perm:          "644",
		FlushCount:    1024,
		FlushInterval: 5000,
		FolderPerm:    "700",
	}
}

func (o *FileOutput) Init(config interface{}) (err error) {
	conf := config.(*FileOutputConfig)
	o.FileOutputConfig = conf
	var intPerm int64

	if intPerm, err = strconv.ParseInt(conf.FolderPerm, 8, 32); err != nil {
		err = fmt.Errorf("FileOutput '%s' can't parse `folder_perm`, is it an octal integer string?",
			o.Folder)
		return err
	}
	o.folderPerm = os.FileMode(intPerm)

	if intPerm, err = strconv.ParseInt(conf.Perm, 8, 32); err != nil {
		err = fmt.Errorf("FileOutput can't parse `perm`, is it an octal integer string?")
		return err
	}
	o.perm = os.FileMode(intPerm)

	if conf.FlushCount < 1 {
		err = fmt.Errorf("Parameter 'flush_count' needs to be greater 1.")
		return err
	}

	o.lru = NewLRU(32)
	o.lru.OnEvicted = func(ce *cacheEntry) {
		fmt.Printf("%s, LRU evicted: %s\n", time.Now().Format(time.RFC822), ce.key)
		if len(ce.data) > 0 {
			o.writeFile(ce.key, ce.data)
		}
	}

	return nil
}

func (o *FileOutput) decode(msg string) (batch outBatch, err error) {
	var msgPack msgPack
	err = json.Unmarshal([]byte(msg), &msgPack)

	if err != nil {
		fmt.Printf("%s\n", msg)
		return
	}

	fileName := msgPack.FileName
	if len(fileName) == 0 {
		err = errors.New("no file name field in the message")
		return
	}

	batch.path = path.Join(o.folder, fileName)
	batch.data = []byte(msgPack.Log)

	return
}

func (o *FileOutput) Run() error {
	go o.committer()
	return o.receiver()
}

func (o *FileOutput) receiver() error {
	var (
		e     error
		batch outBatch
	)

	inChan := o.recvChan

	ok := true
	for ok {
		select {
		case pack, ok := <-inChan:
			if !ok {
				fmt.Println("Input Channel closed")
				ok = false
				break
			}

			if len(pack) == 0 {
				continue
			}

			if batch, e = o.decode(pack); e != nil {
				e = fmt.Errorf("can't decode: %s", e)
				continue
			}

			o.lru.Lock()
			o.lru.Add(batch)
			o.lru.Unlock()
		}
	}

	return nil
}

func (o *FileOutput) writeFile(path string, data []byte) (err error) {
	basePath := filepath.Dir(path)
	if err = os.MkdirAll(basePath, o.folderPerm); err != nil {
		return fmt.Errorf("Can't create the basepath for the FileOutput plugin: %s", err.Error())
	}

	var file *os.File
	if file, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, o.perm); err != nil {
		fmt.Printf("Can't open file: %s, %s", path, err.Error())
		return
	}

	defer func() {
		file.Sync()
		file.Close()
	}()

	n, err := file.Write(data)
	if err != nil {
		return fmt.Errorf("Can't write to %s: %s", path, err.Error())
	} else if n != len(data) {
		return fmt.Errorf("data loss - truncated output for %s", path)
	}
	return
}

func (o *FileOutput) committer() {
	if o.FlushInterval > 0 {
		d, err := time.ParseDuration(fmt.Sprintf("%dms", o.FlushInterval))
		if err != nil {
			fmt.Printf("can't create flush ticker: %s", err.Error())
			return
		}
		o.flushTicker = time.NewTicker(d)
	}

	for {
		select {
		case <-o.flushTicker.C:
			o.lru.Lock()
			for path, cache := range o.lru.cache {
				ce := cache.Value.(*cacheEntry)
				if len(ce.data) > 0 {
					if err := o.writeFile(path, ce.data); err != nil {
						fmt.Println(err.Error())
					}
					ce.data = ce.data[:0]
				}
			}
			o.lru.Unlock()
		}
	}
}
