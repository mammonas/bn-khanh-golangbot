package bots

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"os"
	token "panicbot/constants/contracts_erc20"
	router "panicbot/constants/contracts_pancake_router"
	"panicbot/utils"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/hibiken/asynq"
)

var client = asynq.NewClient(asynq.RedisClientOpt{Addr: "localhost:6379", DB: 1})

const (
	TypeSniffLiquidition = "sniff:liquid"
)

type sniffLiquidationPayload struct {
	Chain           string
	Dex             string
	TokenAddress    string
	BuyAmount       float64
	BuyGasFee       int64
	BuySlippage     int64
	InitAcceptPrice float64
	SellGasFee      int64
	SellSlippage    int64
	Duration        int64

	// for tracking in db
	ProcessStatus string
	BuyStatus     string
	BuyTx         string
	SellStatus    string
	SellTx        string
}

func NewSniffLiquidTask(chain, dex, tokenAddress string, buyAmount float64, buyGasFee int64, buySlippage int64, initAcceptPrice float64, sellGasFee int64, sellSlippage int64, duration int64) (*asynq.Task, error) {
	payload, err := json.Marshal(
		sniffLiquidationPayload{
			Chain:           chain,
			Dex:             dex,
			TokenAddress:    tokenAddress,
			BuyAmount:       buyAmount,
			BuyGasFee:       buyGasFee,
			BuySlippage:     buySlippage,
			InitAcceptPrice: initAcceptPrice,
			SellGasFee:      sellGasFee,
			SellSlippage:    sellSlippage,
			Duration:        duration,
			ProcessStatus:   "scheduled",
			BuyStatus:       "not_started",
			BuyTx:           "",
			SellStatus:      "not_started",
			SellTx:          "",
		})
	if err != nil {
		return nil, err
	}

	return asynq.NewTask(TypeSniffLiquidition, payload, asynq.MaxRetry(0)), nil
}

func HandleSniffLiquidTask(ctx context.Context, t *asynq.Task) error {
	fmt.Println("On HandleSniffLiquidTask")
	var p sniffLiquidationPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		fmt.Println(err)
		return err
	}

	ObserveLiquid(p)
	return nil
}

func ExecuteSniffing(data map[string]string) bool {
	fmt.Println("Now validate the data")

	chain := data["chain"]
	dex := data["dex"]

	tokenAddress := data["token_address"]
	if tokenAddress == "" || chain == "" || dex == "" {
		fmt.Println("err tokenAddress or chain or dex")
		return false
	}

	buyAmount, err2 := strconv.ParseFloat(data["buy_amount"], 64)
	if err2 != nil || buyAmount <= 0 {
		fmt.Println("err buyAmount")
		return false
	}

	buyGasFee, err3 := strconv.ParseInt(data["buy_gas_fee"], 10, 64)
	if err3 != nil || buyGasFee <= 0 {
		fmt.Println("err buyGasFee")
		return false
	}

	buySlippage, err4 := strconv.ParseInt(data["buy_slippage"], 10, 64)
	if err4 != nil || buySlippage <= 0 {
		fmt.Println("err buySlippage")
		return false
	}

	initAcceptPrice, err5 := strconv.ParseFloat(data["init_accept_price"], 64)
	if err5 != nil || initAcceptPrice <= 0 {
		fmt.Println("err initAcceptPrice")
		return false
	}

	sellGasFee, err6 := strconv.ParseInt(data["sell_gas_fee"], 10, 64)
	if err6 != nil || sellGasFee <= 0 {
		fmt.Println("err sellGasFee")
		return false
	}

	sellSlippage, err7 := strconv.ParseInt(data["sell_slippage"], 10, 64)
	if err7 != nil || sellSlippage <= 0 {
		fmt.Println("err sellSlippage")
		return false
	}

	startDatetime := data["start_datetime"]

	duration, err8 := strconv.ParseInt(data["duration"], 10, 64)
	if err8 != nil || duration <= 0 {
		fmt.Println("err duration")
		return false
	}

	sniffPassword := data["sniff_password"]
	if base64.StdEncoding.EncodeToString([]byte(sniffPassword)) != os.Getenv("SNIFF_PASSWORD") {
		fmt.Println("err sniffPassword")
		return false
	}

	fmt.Println(tokenAddress)
	fmt.Println(buyAmount)
	fmt.Println(buyGasFee)
	fmt.Println(buySlippage)
	fmt.Println(initAcceptPrice)
	fmt.Println(sellGasFee)
	fmt.Println(sellSlippage)
	fmt.Println(startDatetime)
	fmt.Println(duration)
	fmt.Println(sniffPassword)

	sniffTask, errT := NewSniffLiquidTask(chain, dex, tokenAddress, buyAmount, buyGasFee, buySlippage, initAcceptPrice, sellGasFee, sellSlippage, duration)
	if errT != nil {
		log.Fatal(errT)
		return false
	}
	if startDatetime != "" {
		myDate, err := time.Parse("2006-01-02T15:04", startDatetime)
		if err != nil {
			log.Fatal(err)
			infoAdded, errA := client.Enqueue(sniffTask, asynq.Timeout(24*time.Hour))
			if errA != nil {
				log.Fatal(errA)
				return false
			}
			log.Printf(" [*] Successfully enqueued task: %+v", infoAdded)
		} else {
			infoAdded, errA := client.Enqueue(sniffTask, asynq.ProcessAt(myDate), asynq.Timeout(24*time.Hour))
			if errA != nil {
				log.Fatal(errA)
				return false
			}
			fmt.Println("myDate")
			fmt.Println(myDate)
			log.Printf(" [*] Successfully enqueued schedule task: %+v", infoAdded)
		}
	} else {
		infoAdded, errA := client.Enqueue(sniffTask, asynq.Timeout(24*time.Hour))
		if errA != nil {
			log.Fatal(errA)
			return false
		}
		log.Printf(" [*] Successfully enqueued task: %+v", infoAdded)
	}
	utils.SaveToRedis("sniff:"+tokenAddress, sniffTask.Payload())
	return true
}

func DecodeTransactionInputData(contractABI *abi.ABI, data []byte) (string, map[string]interface{}) {
	methodSigData := data[:4]
	inputsSigData := data[4:]
	method, err := contractABI.MethodById(methodSigData)
	if err != nil {
		log.Fatal(err)
	}
	inputsMap := make(map[string]interface{})
	if err := method.Inputs.UnpackIntoMap(inputsMap, inputsSigData); err != nil {
		log.Fatal(err)
	}
	return method.Name, inputsMap
}

func GetSniffTasks() []map[string]any {
	data := utils.ReadMulti("sniff:*")
	data_return := []map[string]any{}

	for _, element := range data {
		eData := map[string]any{}
		err2 := json.Unmarshal([]byte(fmt.Sprint(element)), &eData)
		if err2 != nil {
			fmt.Println(err2)
		} else {
			data_return = append(data_return, eData)
		}
	}

	return data_return
}

func ObserveLiquid(pload sniffLiquidationPayload) {
	fmt.Println(os.Getenv("WSS"))
	client, err := ethclient.Dial(os.Getenv("WSS"))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Chain is")
	fmt.Println(pload.Chain)

	fmt.Println("Dex is")
	fmt.Println(pload.Dex)

	router_add := os.Getenv("PROUTER_ADDRESS")
	if strings.EqualFold(pload.Dex, "APESWAP") {
		router_add = os.Getenv("AROUTER_ADDRESS")
	}
	fmt.Println(router_add)

	tokenAddress := common.HexToAddress(pload.TokenAddress)
	tokenInstance, err := token.NewToken(tokenAddress, client)
	if err != nil {
		fmt.Println("Loi o day ha")
		log.Fatal(err)
	}

	tokenDecimal, err := tokenInstance.Decimals(&bind.CallOpts{})
	if err != nil {
		fmt.Println("Loi o day ha 2222")
		log.Fatal(err)
	}
	fmt.Println("tokenDecimal")
	fmt.Println(tokenDecimal)

	routerAddress := common.HexToAddress(router_add)
	routerInstance, err := router.NewPancake(routerAddress, client)
	if err != nil {
		log.Fatal(err)
	}
	pathBNBUSDT := []common.Address{
		common.HexToAddress(os.Getenv("BNB")),
		common.HexToAddress(os.Getenv("BUSD")),
	}
	priceBNBResult, err := routerInstance.GetAmountsOut(&bind.CallOpts{}, big.NewInt(1), pathBNBUSDT)
	if err != nil {
		log.Fatal(err)
	}

	priceBNB := priceBNBResult[1]
	fmt.Println("priceBNB")
	fmt.Println(priceBNB)

	thisTime := time.Now().UTC()
	deadTime := thisTime.Add(time.Duration(pload.Duration) * time.Minute)

	pload.ProcessStatus = "running"
	mar, errMar := json.Marshal(pload)
	if errMar != nil {
		fmt.Println("Error in json.Marshal")
		fmt.Println(errMar)
	} else {
		utils.SaveToRedis("sniff:"+pload.TokenAddress, mar)
	}

	routerABIBytes, err := os.ReadFile("./constants/abis/pancake_router.abi")
	if err != nil {
		fmt.Print(err)
	}
	strRouterABI := string(routerABIBytes)

	routerAbi, err := abi.JSON(strings.NewReader(strRouterABI))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("before")
	initNoneAndChain(client, pload.Dex)
	fmt.Println("after_initNoneAndChain")

	buyToken := ""
	stableToken := ""
	buyTokenAmount := big.NewInt(0)
	stableTokenAmount := big.NewInt(0)
	acceptStableAmount, _ := new(big.Float).SetString(os.Getenv("ACCEPT_LIQUID"))
	isAccept := false
	inLoop := true
	startObserveTime := time.Now()

	headers := make(chan *types.Header)
	sub, err := client.SubscribeNewHead(context.Background(), headers)
	if err != nil {
		log.Fatal(err)
	}

	for inLoop {
		select {
		case err := <-sub.Err():
			log.Fatal(err)
		case header := <-headers:
			fmt.Println("Observing...")
			if time.Now().UTC().After(deadTime) {
				fmt.Println("\nLater, break")
				pload.ProcessStatus = "finished"
				mar, errMar := json.Marshal(pload)
				if errMar != nil {
					fmt.Println("Error in json.Marshal 2")
					fmt.Println(errMar)
					return
				} else {
					utils.SaveToRedis("sniff:"+pload.TokenAddress, mar)
				}

				inLoop = false
				break
			}

			block, err := client.BlockByHash(context.Background(), header.Hash())
			if err != nil {
				log.Fatal(err)
			}
			fmt.Println("In here")
			fmt.Println(block.Number())
			for _, tx := range block.Transactions() {
				if tx.To() != nil && strings.EqualFold(tx.To().Hex(), router_add) {
					methodName, inputMap := DecodeTransactionInputData(&routerAbi, tx.Data())
					if methodName == "addLiquidity" {
						fmt.Println("Found an addLiquidity")
						// pload.TokenAddress = fmt.Sprintf("%v", inputMap["tokenA"]) // cheat
						startObserveTime = time.Now()
						dataTokenA := fmt.Sprintf("%v", inputMap["tokenA"])
						dataTokenB := fmt.Sprintf("%v", inputMap["tokenB"])
						dataAmountA := fmt.Sprintf("%v", inputMap["amountADesired"])
						dataAmountB := fmt.Sprintf("%v", inputMap["amountBDesired"])
						if strings.EqualFold(dataTokenA, pload.TokenAddress) {
							buyToken = dataTokenA
							stableToken = dataTokenB
							fmt.Sscan(dataAmountA, buyTokenAmount)
							fmt.Sscan(dataAmountB, stableTokenAmount)
						} else if strings.EqualFold(dataTokenB, pload.TokenAddress) {
							buyToken = dataTokenB
							stableToken = dataTokenA
							fmt.Sscan(dataAmountB, buyTokenAmount)
							fmt.Sscan(dataAmountA, stableTokenAmount)
						}
						compare := getNumberOfToken(stableTokenAmount, 18).Cmp(acceptStableAmount)
						if strings.EqualFold(stableToken, os.Getenv("BNB")) {
							compare = getUSDFromBNB(stableTokenAmount, priceBNB).Cmp(acceptStableAmount)
						}
						fmt.Println("compare")
						fmt.Println(compare)
						if compare >= 0 {
							fmt.Println("accept addLiquidity")
							isAccept = true
							inLoop = false
							break
						}
					} else if methodName == "addLiquidityETH" {
						fmt.Println("Found an addLiquidityETH")
						startObserveTime = time.Now()
						dataToken := fmt.Sprintf("%v", inputMap["token"])
						amountToken := fmt.Sprintf("%v", inputMap["amountTokenDesired"])
						// pload.TokenAddress = dataToken
						if strings.EqualFold(dataToken, pload.TokenAddress) {
							buyToken = dataToken
							stableToken = os.Getenv("BNB")
							fmt.Sscan(amountToken, buyTokenAmount)
							fmt.Sscan(fmt.Sprintf("%v", inputMap["amountETHMin"]), stableTokenAmount)
						}
						compare := getUSDFromBNB(stableTokenAmount, priceBNB).Cmp(acceptStableAmount)
						if compare >= 0 {
							fmt.Println("accept addLiquidityETH")
							isAccept = true
							inLoop = false
							break
						}
					}
				}
			}

			if isAccept && !strings.EqualFold(buyToken, "") {
				fmt.Println("Found it")
				fmt.Println(buyToken)
				fmt.Println(stableToken)
				fmt.Println(buyTokenAmount)
				fmt.Println(stableTokenAmount)

				numberToken := getNumberOfToken(buyTokenAmount, tokenDecimal)
				numberStable := getNumberOfToken(stableTokenAmount, 18)
				fmt.Println(numberToken)
				fmt.Println(numberStable)
				thuong := numberStable
				if strings.EqualFold(stableToken, os.Getenv("BNB")) {
					thuong = getUSDFromBNB(stableTokenAmount, priceBNB)
				}
				priceAtLiquid := new(big.Float).Quo(thuong, numberToken)
				fmt.Println("priceAtLiquid")
				fmt.Println(priceAtLiquid)
				elapsed := time.Since(startObserveTime)
				log.Printf("Took %s when Found", elapsed)
				if priceAtLiquid.Cmp(big.NewFloat(pload.InitAcceptPrice)) <= 0 {
					fmt.Println("Init Price less than or equal acceptPrice, execute buy")
					ExecuteSniffBuyingThenSelling(client, pload, routerInstance, tokenInstance, buyToken, stableToken, priceBNB, priceAtLiquid, tokenDecimal)
				} else {
					pload.ProcessStatus = "cancel:inittoohigh"
					saveStatusToRedis(pload)
				}

			}
		}
	}
}

func saveStatusToRedis(pload sniffLiquidationPayload) {
	mar, errMar := json.Marshal(pload)
	if errMar != nil {
		fmt.Println("Error in json.Marshal")
		fmt.Println(errMar)
	} else {
		utils.SaveToRedis("sniff:"+pload.TokenAddress, mar)
	}
}

func getNumberOfToken(amount *big.Int, decimal uint8) *big.Float {
	amountF := new(big.Float).SetInt(amount)
	base := big.NewInt(10)
	po := big.NewInt(int64(decimal))
	basePO := base.Exp(base, po, nil)
	rs := new(big.Float).Quo(amountF, new(big.Float).SetInt(basePO))
	return rs
}

func getUSDFromBNB(amount *big.Int, priceBNB *big.Int) *big.Float {
	tokenNum := getNumberOfToken(amount, 18)
	priceFloat := new(big.Float).SetInt(priceBNB)
	USDPrice := new(big.Float).Mul(tokenNum, priceFloat)
	fmt.Println("Total Price of BNB is")
	fmt.Println(USDPrice)
	return USDPrice
}
