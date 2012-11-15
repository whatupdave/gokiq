package gokiq

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/garyburd/redigo/redis"
)

var Client = NewClientConfig()

type jobMap map[reflect.Type]JobConfig

type ClientConfig struct {
	RedisServer    string
	RedisNamespace string
	RedisMaxIdle   int

	redisPool   *redis.Pool
	jobMapping  jobMap
	knownQueues map[string]bool
}

func NewClientConfig() *ClientConfig {
	return &ClientConfig{
		RedisServer:  defaultRedisServer,
		RedisMaxIdle: 1,
		jobMapping:   make(jobMap),
		knownQueues:  make(map[string]bool),
	}
}

func (c *ClientConfig) Register(name string, worker Worker, queue string, retries int) {
	c.jobMapping[workerType(worker)] = JobConfig{queue, retries, name}
	c.trackQueue(queue)
}

func (c *ClientConfig) Connect() {
	// TODO: add a mutex for the redis pool
	if c.redisPool != nil {
		c.redisPool.Close()
	}
	c.redisPool = redis.NewPool(func() (redis.Conn, error) {
		return redis.Dial("tcp", c.RedisServer)
	}, c.RedisMaxIdle)

	queues := make([]interface{}, 1, len(c.knownQueues)+1)
	queues[0] = c.nsKey("queues")
	for queue := range c.knownQueues {
		queues = append(queues, queue)
	}
	c.redisQuery("SADD", queues...)
}

func (c *ClientConfig) QueueJob(worker Worker, args ...interface{}) error {
	config, ok := c.jobMapping[workerType(worker)]
	if !ok {
		panic(fmt.Errorf("gokiq: Unregistered worker type %T", worker))
	}
	return c.queueJob(config.name, config, args)
}

func (c *ClientConfig) QueueJobWithConfig(name string, config JobConfig, args ...interface{}) error {
	c.trackQueue(config.Queue)
	return c.queueJob(name, config, args)
}

func (c *ClientConfig) queueJob(name string, config JobConfig, args []interface{}) error {
	if args == nil {
		args = make([]interface{}, 0) // json encodes nil slices as null
	}
	job := &Job{
		Type:  name,
		Args:  args,
		Retry: config.MaxRetries,
		ID:    generateJobID(),
	}
	json, err := json.Marshal(job)
	if err != nil {
		return err
	}

	_, err = c.redisQuery("RPUSH", c.nsKey("queue:"+config.Queue), json)
	return err
}

func (c *ClientConfig) trackQueue(queue string) {
	_, known := c.knownQueues[queue]
	if !known {
		c.knownQueues[queue] = true
		if c.redisPool != nil {
			c.redisQuery("SADD", c.nsKey("queues"), queue)
		}
	}
}

func (c *ClientConfig) redisQuery(command string, args ...interface{}) (interface{}, error) {
	conn := c.redisPool.Get()
	defer conn.Close()
	return conn.Do(command, args...)
}

func (c *ClientConfig) nsKey(key string) string {
	if c.RedisNamespace != "" {
		return c.RedisNamespace + ":" + key
	}
	return key
}

func generateJobID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

type JobConfig struct {
	Queue      string
	MaxRetries int

	name string
}