package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"panicbot/bots"
	"panicbot/utils"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/hibiken/asynqmon"
	"github.com/joho/godotenv"
)

func setupGin() *gin.Engine {
	router := gin.Default()
	router.Static("/css", "templates/css")
	router.LoadHTMLGlob("templates/*.html")

	router.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "Hi there ^^")
	})

	authorized := router.Group("/", gin.BasicAuth(gin.Accounts{
		"khanh": "deptrai",
	}))
	authorized.GET("/sniff_liquid", func(c *gin.Context) {
		c.HTML(http.StatusOK, "sniff_liquid.html", nil)
	})

	router.GET("/sniff_tasks", func(c *gin.Context) {
		fmt.Println("sniff_tasks")
		c.JSON(http.StatusOK, gin.H{"tasks": bots.GetSniffTasks()})
	})

	router.POST("/sniff_liquid", func(c *gin.Context) {
		jsonData, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"sniff_result": "failed"})
		}
		data := map[string]string{}
		err2 := json.Unmarshal([]byte(jsonData), &data)
		if err2 != nil {
			c.JSON(http.StatusBadRequest, gin.H{"sniff_result": "failed"})
		}
		is_executes := bots.ExecuteSniffing(data)
		if !is_executes {
			fmt.Println("Failed here")
			c.JSON(http.StatusBadRequest, gin.H{"sniff_result": "failed"})
		}

		c.JSON(http.StatusOK, gin.H{"sniff_result": "submitted"})
	})

	router.POST("/approve_spending", func(c *gin.Context) {
		jsonData, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"approve_result": "failed"})
		}
		data := map[string]string{}
		err2 := json.Unmarshal([]byte(jsonData), &data)
		if err2 != nil {
			c.JSON(http.StatusBadRequest, gin.H{"approve_result": "failed"})
		}
		is_submmited := bots.ExecuteApproveSpending(data)
		if !is_submmited {
			fmt.Println("Failed here")
			c.JSON(http.StatusOK, gin.H{"approve_result": "failed"})
		} else {
			c.JSON(http.StatusOK, gin.H{"approve_result": "approve submitted"})
		}
	})

	router.POST("/panic_sell", func(c *gin.Context) {
		jsonData, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"sell_result": "failed"})
		}
		data := map[string]string{}
		err2 := json.Unmarshal([]byte(jsonData), &data)
		if err2 != nil {
			c.JSON(http.StatusBadRequest, gin.H{"sell_result": "failed"})
		}
		is_submmited := bots.ExecuteAddTaskSell(data)
		if !is_submmited {
			fmt.Println("Failed here")
			c.JSON(http.StatusOK, gin.H{"sell_result": "failed"})
		} else {
			c.JSON(http.StatusOK, gin.H{"sell_result": "sell submitted"})
		}
	})

	router.GET("/approve_sell_task", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"tasks": bots.GetApproveSellTasks()})
	})

	router.POST("/execute_clean", func(c *gin.Context) {
		jsonData, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"result": "cannot read body"})
		}
		data := map[string]string{}
		err2 := json.Unmarshal([]byte(jsonData), &data)
		if err2 != nil {
			c.JSON(http.StatusOK, gin.H{"result": "cannot parse json"})
		}

		if base64.StdEncoding.EncodeToString([]byte(data["clean_password"])) != os.Getenv("SNIFF_PASSWORD") {
			fmt.Println("err sniffPassword")
			fmt.Println(data["clean_password"])
			c.JSON(http.StatusOK, gin.H{"result": "incorrect password"})
		} else {
			utils.FlushDB()
			c.JSON(http.StatusOK, gin.H{"result": "cleaned all Redis"})
		}
	})

	authorized.GET("/panic_sell", func(c *gin.Context) {
		c.HTML(http.StatusOK, "panic_sell.html", nil)
	})

	h := asynqmon.New(asynqmon.Options{
		RootPath:     "/monitor", // RootPath specifies the root for asynqmon app
		RedisConnOpt: asynq.RedisClientOpt{Addr: ":6379", DB: 1},
	})
	authorized.Any("/monitor/*a", gin.WrapH(h))

	return router
}

func main() {

	if strings.EqualFold(gin.Mode(), gin.ReleaseMode) {
		godotenv.Load("./.env.release")
	} else if strings.EqualFold(gin.Mode(), gin.DebugMode) {
		godotenv.Load("./.env.debug")
	}

	r := setupGin()
	ginSvr := &http.Server{
		Addr:    ":8989",
		Handler: r,
	}
	aSrv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: "localhost:6379", DB: 1},
		asynq.Config{Concurrency: 10},
	)
	mux := asynq.NewServeMux()
	mux.HandleFunc(bots.TypeSniffLiquidition, bots.HandleSniffLiquidTask)
	mux.HandleFunc(bots.TypeApprove, bots.HandleApproveTask)
	mux.HandleFunc(bots.TypeSell, bots.HandleSellTask)
	if err := aSrv.Start(mux); err != nil {
		log.Fatal(err)
	}
	go func() {
		utils.PingRedis()
		fmt.Println("Server Mode: ", gin.Mode())
		if err := ginSvr.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
		log.Println("HTTP Server started")
	}()

	quit := make(chan os.Signal)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutdown Server ...")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := ginSvr.Shutdown(ctx); err != nil {
		log.Fatal("Gin Server Shutdown:", err)
	}
	aSrv.Shutdown()
	log.Println("Mux Server exiting")
	utils.FlushDB()
	// catching ctx.Done(). timeout of 5 seconds.
	select {
	case <-ctx.Done():
		log.Println("timeout of 2 seconds.")
	}
	log.Println("Servers exiting")
}
