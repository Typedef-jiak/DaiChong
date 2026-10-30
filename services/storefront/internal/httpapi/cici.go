package httpapi

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"gptibo/storefront/internal/auth"
	"gptibo/storefront/internal/cici"
	"gptibo/storefront/internal/fault"
)

func registerCici(admin *gin.RouterGroup, s *cici.Service) {
	actor := func(c *gin.Context) string { return c.MustGet("principal").(auth.Principal).Subject }
	reply := func(c *gin.Context, v any, err error) {
		if err != nil {
			var upstream *cici.APIError
			if errors.As(err, &upstream) {
				c.AbortWithStatusJSON(502, gin.H{"error": "Cici 请求未完成：" + upstream.Code + "。请核对配置或原采购记录。", "code": upstream.Code})
				return
			}
			fail(c, err)
			return
		}
		c.JSON(200, v)
	}
	admin.GET("/cici", func(c *gin.Context) {
		c.JSON(200, gin.H{"configured": s.Client != nil, "purchasesEnabled": s.Client != nil && s.PurchasesEnabled, "protocol": "1.1"})
	})
	admin.GET("/cici/ping", func(c *gin.Context) {
		if err := s.Ready(); err != nil {
			fail(c, err)
			return
		}
		v, err := s.Client.Ping(c.Request.Context())
		reply(c, v, err)
	})
	admin.GET("/cici/products", func(c *gin.Context) {
		if err := s.Ready(); err != nil {
			fail(c, err)
			return
		}
		v, err := s.Client.Products(c.Request.Context(), page(c))
		reply(c, v, err)
	})
	admin.GET("/cici/products/:id", func(c *gin.Context) {
		if err := s.Ready(); err != nil {
			fail(c, err)
			return
		}
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			fail(c, fault.New(400, "商品 ID 无效。"))
			return
		}
		v, err := s.Client.Product(c.Request.Context(), id)
		reply(c, v, err)
	})
	admin.POST("/cici/orders", func(c *gin.Context) {
		var in cici.DraftInput
		if !read(c, &in) {
			return
		}
		v, err := s.Draft(c.Request.Context(), in, actor(c))
		reply(c, gin.H{"purchase": v}, err)
	})
	admin.GET("/cici/orders", func(c *gin.Context) {
		v, total, err := s.List(c.Request.Context(), page(c))
		reply(c, gin.H{"items": v, "total": total, "page_size": 25}, err)
	})
	admin.GET("/cici/orders/:id", func(c *gin.Context) { v, err := s.Detail(c.Request.Context(), c.Param("id")); reply(c, v, err) })
	admin.POST("/cici/orders/:id/submit", func(c *gin.Context) {
		var in struct {
			Confirmed bool   `json:"confirmed"`
			Amount    string `json:"expected_amount"`
		}
		if !read(c, &in) {
			return
		}
		if !in.Confirmed {
			fail(c, fault.New(400, "请先确认采购金额及 Cici 钱包扣款。"))
			return
		}
		v, err := s.Submit(c.Request.Context(), c.Param("id"), in.Amount, actor(c))
		reply(c, gin.H{"purchase": v}, err)
	})
	admin.POST("/cici/orders/:id/refresh", func(c *gin.Context) {
		var in struct{}
		if !read(c, &in) {
			return
		}
		v, err := s.Refresh(c.Request.Context(), c.Param("id"), actor(c))
		reply(c, gin.H{"purchase": v}, err)
	})
	admin.POST("/cici/orders/:id/cancel", func(c *gin.Context) {
		var in struct {
			Confirmed bool `json:"confirmed"`
		}
		if !read(c, &in) {
			return
		}
		if !in.Confirmed {
			fail(c, fault.New(400, "请先确认取消采购。"))
			return
		}
		v, err := s.Cancel(c.Request.Context(), c.Param("id"), actor(c))
		reply(c, gin.H{"purchase": v}, err)
	})
}
