// cici is a read-only local diagnostic command. Purchases require the durable admin workflow.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"gptibo/storefront/internal/cici"
)

func run() error {
	keyFile := flag.String("key-file", ".secrets/cici-api-key", "本机 API Key 文件")
	secretFile := flag.String("secret-file", ".secrets/cici-api-secret", "本机 API Secret 文件")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		return fmt.Errorf("用法：go run ./cmd/cici [配置选项] ping|products [页码]|product ID|quote SKU 数量")
	}
	key, err := os.ReadFile(*keyFile)
	if err != nil {
		return fmt.Errorf("无法读取 API Key 文件，请先填写本机 .secrets/cici-api-key")
	}
	secret, err := os.ReadFile(*secretFile)
	if err != nil {
		return fmt.Errorf("无法读取 API Secret 文件，请先填写本机 .secrets/cici-api-secret")
	}
	client, err := cici.NewClient(trimLine(key), trimLine(secret))
	if err != nil {
		return fmt.Errorf("凭据为空或格式无效")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	var out any
	switch args[0] {
	case "ping":
		if len(args) != 1 {
			return fmt.Errorf("ping 不需要参数")
		}
		out, err = client.Ping(ctx)
	case "products":
		page := 1
		if len(args) > 2 {
			return fmt.Errorf("products 只接受页码")
		}
		if len(args) == 2 {
			page, err = strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("页码必须为正整数")
			}
		}
		out, err = client.Products(ctx, page)
	case "product":
		if len(args) != 2 {
			return fmt.Errorf("product 需要商品 ID")
		}
		id, e := strconv.ParseInt(args[1], 10, 64)
		if e != nil {
			return fmt.Errorf("商品 ID 无效")
		}
		out, err = client.Product(ctx, id)
	case "quote":
		if len(args) != 3 {
			return fmt.Errorf("quote 需要 SKU ID 和数量")
		}
		id, e := strconv.ParseInt(args[1], 10, 64)
		if e != nil {
			return fmt.Errorf("SKU ID 无效")
		}
		quantity, e := strconv.Atoi(args[2])
		if e != nil {
			return fmt.Errorf("数量无效")
		}
		out, err = client.Quote(ctx, id, quantity)
	default:
		return fmt.Errorf("只支持 ping、products、product、quote；本命令不提供采购扣款操作")
	}
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(out)
}
func trimLine(b []byte) string {
	for len(b) > 0 && (b[len(b)-1] == '\r' || b[len(b)-1] == '\n') {
		b = b[:len(b)-1]
	}
	return string(b)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
