package query

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/config"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/debug"
)

// Params are used during calls to the RPC.
type Params []interface{}

// Payload is used to make calls to the RPC.
type Payload struct {
	Headers map[string]string `json:"headers,omitempty"`
	Method  string            `json:"method"`
	Params  `json:"params"`
}

type rpcResponse[T any] struct {
	Result T             `json:"result"`
	Error  *eip1474Error `json:"error"`
}

type rpcBatchResponse[T any] struct {
	ID     int           `json:"id"`
	Result *T            `json:"result"`
	Error  *eip1474Error `json:"error"`
}

type eip1474Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var rpcCounter uint32

type rpcPayload struct {
	Jsonrpc string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  `json:"params"`
	ID      int `json:"id"`
}

// BatchPayload is a wrapper around Payload type that allows us
// to associate a name (Key) to given request.
type BatchPayload struct {
	Key string
	*Payload
}

// Query returns a single result for given method and params.
func Query[T any](chain string, method string, params Params) (*T, error) {
	url := config.GetChain(chain).GetRpcProvider()
	return QueryUrl[T](url, method, params)
}

// QueryUrl is just like Query, but it does not resolve chain to RPC provider URL
func QueryUrl[T any](url string, method string, params Params) (*T, error) {
	return QueryWithHeaders[T](url, map[string]string{}, method, params)
}

// QueryWithHeaders returns a single result for a given method and params.
func QueryWithHeaders[T any](url string, headers map[string]string, method string, params Params) (*T, error) {
	payloadToSend := rpcPayload{
		Jsonrpc: "2.0",
		Method:  method,
		Params:  params,
		ID:      int(uint32(atomic.AddUint32(&rpcCounter, 1))),
	}

	debug.DebugCurl(rpcDebug{url: url, payload: payloadToSend, headers: headers})

	if plBytes, err := json.Marshal(payloadToSend); err != nil {
		return nil, err
	} else {
		body := bytes.NewReader(plBytes)
		if request, err := http.NewRequest("POST", url, body); err != nil {
			return nil, err
		} else {
			request.Header.Set("Content-Type", "application/json")
			for key, value := range headers {
				request.Header.Set(key, value)
			}

			client := &http.Client{}
			if response, err := client.Do(request); err != nil {
				return nil, err
			} else if response.StatusCode != 200 {
				return nil, fmt.Errorf("%s: %d", response.Status, response.StatusCode)
			} else {
				defer response.Body.Close()

				if theBytes, err := io.ReadAll(response.Body); err != nil {
					return nil, err
				} else {
					var result rpcResponse[T]
					if err = json.Unmarshal(theBytes, &result); err != nil {
						return nil, err
					} else {
						if result.Error != nil {
							return nil, fmt.Errorf("%d: %s", result.Error.Code, result.Error.Message)
						}
						return &result.Result, nil
					}
				}
			}
		}
	}
}

// QueryBatch batches requests to the node. Returned values are stored in map, with the same keys as defined
// in `batchPayload` (this way we don't have to operate on array indices)
func QueryBatch[T any](chain string, batchPayload []BatchPayload) (map[string]*T, error) {
	return QueryBatchWithHeaders[T](chain, map[string]string{}, batchPayload)
}

func QueryBatchWithHeaders[T any](chain string, headers map[string]string, batchPayload []BatchPayload) (map[string]*T, error) {
	if len(batchPayload) == 0 {
		return map[string]*T{}, nil
	}

	keysByID := make(map[int]string, len(batchPayload))
	methodsByID := make(map[int]string, len(batchPayload))
	for _, bpl := range batchPayload {
		if bpl.Payload == nil {
			return nil, fmt.Errorf("batch payload %q is nil", bpl.Key)
		}
	}

	url := config.GetChain(chain).GetRpcProvider()
	payloadToSend := make([]rpcPayload, 0, len(batchPayload))

	for _, bpl := range batchPayload {
		payload := *bpl.Payload
		id := int(atomic.AddUint32(&rpcCounter, 1))
		theLoad := rpcPayload{
			Jsonrpc: "2.0",
			Method:  payload.Method,
			Params:  payload.Params,
			ID:      id,
		}
		keysByID[id] = bpl.Key
		methodsByID[id] = payload.Method
		debug.DebugCurl(rpcDebug{
			url:     url,
			payload: theLoad,
			headers: headers,
		})
		payloadToSend = append(payloadToSend, theLoad)
	}

	plBytes, err := json.Marshal(payloadToSend)
	if err != nil {
		return nil, err
	}

	var result []rpcBatchResponse[T]
	body := bytes.NewReader(plBytes)
	if request, err := http.NewRequest("POST", url, body); err != nil {
		return nil, err
	} else {
		request.Header.Set("Content-Type", "application/json")
		for key, value := range headers {
			request.Header.Set(key, value)
		}

		client := &http.Client{}
		if response, err := client.Do(request); err != nil {
			return nil, err
		} else if response.StatusCode != http.StatusOK {
			defer response.Body.Close()
			return nil, fmt.Errorf("%s: %d", response.Status, response.StatusCode)
		} else {
			defer response.Body.Close()
			theBytes, err := io.ReadAll(response.Body)
			if err != nil {
				return nil, err
			}
			if err = json.Unmarshal(theBytes, &result); err != nil {
				return nil, err
			}
			if len(result) != len(batchPayload) {
				return nil, fmt.Errorf("batch response count %d does not match request count %d", len(result), len(batchPayload))
			}

			results := make(map[string]*T, len(batchPayload))
			seen := make(map[int]bool, len(batchPayload))
			for index := range result {
				response := &result[index]
				key, ok := keysByID[response.ID]
				if !ok {
					return nil, fmt.Errorf("batch response returned unknown id %d", response.ID)
				}
				if seen[response.ID] {
					return nil, fmt.Errorf("batch response returned duplicate id %d", response.ID)
				}
				seen[response.ID] = true

				method := methodsByID[response.ID]
				if response.Error != nil {
					return nil, fmt.Errorf("batch %s (%s) failed: %d: %s", key, method, response.Error.Code, response.Error.Message)
				}
				if response.Result == nil {
					return nil, fmt.Errorf("batch %s (%s) returned no result", key, method)
				}
				results[key] = response.Result
			}
			return results, nil
		}
	}
}

func init() {
	// We need to increase MaxIdleConnsPerHost, otherwise chifra will keep trying to open too
	// many ports. It can lead to bind errors.
	// The default value is too low, so Go closes ports too fast. In the meantime, chifra tries
	// to get new ones and so it can run out of available ports.
	//
	// We change DefaultTransport as the whole codebase uses it.
	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = runtime.GOMAXPROCS(0) * 4
}

type rpcDebug struct {
	payload rpcPayload
	url     string
	headers map[string]string
}

func (c rpcDebug) Url() string {
	return c.url
}

func (c rpcDebug) Body() string {
	return `curl -X POST [{headers}] --data '[{payload}]' [{url}]`
}

func (c rpcDebug) Headers() string {
	ret := `-H "Content-Type: application/json"`
	for key, value := range c.headers {
		ret += fmt.Sprintf(` -H "%s: %s"`, key, value)
	}
	return ret
}

func (c rpcDebug) Method() string {
	return c.payload.Method
}

func (c rpcDebug) Payload() string {
	bytes, _ := json.MarshalIndent(c.payload, "", "")
	payloadStr := strings.ReplaceAll(string(bytes), "\n", " ")
	return payloadStr
}
