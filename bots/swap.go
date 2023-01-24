package bots

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/exp/slices"

	token "panicbot/constants/contracts_erc20"
	factory "panicbot/constants/contracts_pancake_factory"
	liquid "panicbot/constants/contracts_pancake_liquid"
	router "panicbot/constants/contracts_pancake_router"

	consts "panicbot/constants"
	"panicbot/utils"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/hibiken/asynq"
)

var i, e = big.NewInt(10), big.NewInt(18)
var tokenBasedDecimal = i.Exp(i, e, nil)

const (
	TypeApprove = "approve:"
	TypeSell    = "sell:"
)

func HandleSellTask(ctx context.Context, t *asynq.Task) error {
	fmt.Println("On HandleSellTask")
	var p approveSellPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		fmt.Println(err)
		return err
	}

	ObserveAndSell(p)
	return nil
}

func ObserveAndSell(pload approveSellPayload) {
	ethClient, err := ethclient.Dial(os.Getenv("WSS"))
	if err != nil {
		log.Fatal(err)
	}
	pload.SellStatus = "Observing Liquid"
	saveApproveStatusToRedis(pload)
	initNoneAndChain(ethClient, pload.Dex)

	router_add := os.Getenv("PROUTER_ADDRESS")
	if strings.EqualFold(pload.Dex, "APESWAP") {
		router_add = os.Getenv("AROUTER_ADDRESS")
	}
	fmt.Println(router_add)
	router_address = common.HexToAddress(router_add)

	tokenAddress := common.HexToAddress(pload.TokenAddress)
	tokenInstance, err := token.NewToken(tokenAddress, ethClient)
	if err != nil {
		log.Fatal(err)
	}
	// infinity loop: check liquid
	pathBNBUSDT := []common.Address{
		common.HexToAddress(os.Getenv("BNB")),
		common.HexToAddress(os.Getenv("BUSD")),
	}
	routerInstance, err := router.NewPancake(router_address, ethClient)
	if err != nil {
		log.Fatal(err)
	}
	priceBNBResult, err := routerInstance.GetAmountsOut(&bind.CallOpts{}, big.NewInt(1), pathBNBUSDT)
	if err != nil {
		log.Fatal(err)
	}

	priceBNB := priceBNBResult[1]
	fmt.Println("priceBNB")
	fmt.Println(priceBNB)

	// infinity loop: check liquid
	pathToSell, isETH := GetGoodPair(ethClient, tokenAddress, priceBNB, pload.Dex)
	fmt.Println("pathToSell")
	fmt.Println(pathToSell)

	// infinity loop: check balances
	pload.SellStatus = "Observing Balance"
	saveApproveStatusToRedis(pload)
	zeroBalance := big.NewInt(0)
	balance, err := tokenInstance.BalanceOf(&bind.CallOpts{}, walletAddress)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Balance before waiting")
	fmt.Println(balance)
	if balance.Cmp(zeroBalance) <= 0 {
		for {
			balance, err = tokenInstance.BalanceOf(&bind.CallOpts{}, walletAddress)
			if err != nil {
				log.Fatal(err)
				break
			}
			if balance.Cmp(zeroBalance) == 1 {
				break
			}
			time.Sleep(sleepTime)
		}
	}
	fmt.Println("Balance after waiting")
	fmt.Println(balance)
	// prepare data to save time: dont do this
	opts := buildCustomDataToSend(ethClient, pload.SellGasFee, big.NewInt(0))
	// perform swap
	deadline := big.NewInt(time.Now().UTC().Unix() + 1000000)
	if isETH {
		tx, errSwap := routerInstance.SwapExactTokensForETHSupportingFeeOnTransferTokens(opts, balance, zeroBalance, pathToSell, walletAddress, deadline)
		pload.SellStatus = "SentTX"
		if errSwap != nil {
			fmt.Println("Error sending Trans to sell")
			fmt.Println(errSwap)
			pload.SellStatus = "Sell: Failed"
		} else {
			pload.SellTx = os.Getenv("SCAN_URL") + tx.Hash().Hex()
			saveApproveStatusToRedis(pload)
		}
	} else {
		tx, errSwap := routerInstance.SwapExactTokensForTokensSupportingFeeOnTransferTokens(opts, balance, zeroBalance, pathToSell, walletAddress, deadline)
		if errSwap != nil {
			fmt.Println("Error sending Trans to sell")
			fmt.Println(err)
			pload.SellStatus = "Sell: Failed"
		} else {
			pload.SellTx = os.Getenv("SCAN_URL") + tx.Hash().Hex()
			saveApproveStatusToRedis(pload)
		}
	}
}

func GetGoodPair(ethClient *ethclient.Client, tokenAddress common.Address, priceBNB *big.Int, dex string) ([]common.Address, bool) {
	result := []common.Address{
		tokenAddress,
	}
	stableList := []common.Address{
		common.HexToAddress(os.Getenv("BNB")),
		common.HexToAddress(os.Getenv("BUSD")),
		common.HexToAddress(os.Getenv("USDT")),
	}
	plainBNB := os.Getenv("BNB")
	expectedUSDAmount, _ := new(big.Int).SetString(os.Getenv("ACCEPT_LIQUID"), 10)
	isETH := false
	factory_add := os.Getenv("PFACTORY_ADDRESS")
	if strings.EqualFold(dex, "APESWAP") {
		factory_add = os.Getenv("AFACTORY_ADDRESS")
	}
	factoryInstance, err := factory.NewFactory(common.HexToAddress(factory_add), ethClient)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Infinity Loop :D")
inf_loop:
	for {
		for _, addr := range stableList {
			fmt.Println("Pair")
			fmt.Println(addr.Hex())
			isETH = false
			liquidAddress, err := factoryInstance.GetPair(&bind.CallOpts{}, tokenAddress, addr)
			if err != nil {
				fmt.Println("Loi cho nay ha ???")
				log.Fatal(err)
			}
			if !strings.EqualFold(liquidAddress.Hex(), "0x0000000000000000000000000000000000000000") {
				fmt.Println("Found candidate liquid contract")
				fmt.Println(liquidAddress.Hex())
				// now check LP contract to get
				liquidInstance, err := liquid.NewLiquid(liquidAddress, ethClient)
				if err != nil {
					log.Fatal(err)
				}
				token0, err0 := liquidInstance.Token0(&bind.CallOpts{})
				if err0 != nil {
					log.Fatal(err0)
				}
				fmt.Println(token0.Hex())
				token1, err1 := liquidInstance.Token1(&bind.CallOpts{})
				if err1 != nil {
					log.Fatal(err1)
				}
				fmt.Println(token1.Hex())
				reserves, errR := liquidInstance.GetReserves(&bind.CallOpts{})
				if errR != nil {
					log.Fatal(errR)
				}
				fmt.Println(reserves)
				stableToken := token1
				amountStableCoin := reserves.Reserve1
				if slices.Contains(stableList, token0) {
					stableToken = token0
					amountStableCoin = reserves.Reserve0
				}

				amountUSD := new(big.Int).Div(amountStableCoin, tokenBasedDecimal)
				fmt.Println("amountStableCoin")
				fmt.Println(amountStableCoin)
				fmt.Println("amountUSD")
				fmt.Println(amountUSD)
				isBNB := strings.EqualFold(stableToken.Hex(), plainBNB)
				if isBNB {
					isETH = true
					amountUSD = amountUSD.Mul(amountUSD, priceBNB)
					fmt.Println("amountUSD")
					fmt.Println(amountUSD)
				}
				if amountUSD.Cmp(expectedUSDAmount) >= 0 {
					fmt.Println("GOOOODDDD LIQUID")
					result = append(result, stableToken)
					break inf_loop
				}
			}
			time.Sleep(sleepTime)
		}
		time.Sleep(sleepTime)
	}

	return result, isETH
}

func ExecuteAddTaskSell(data map[string]string) bool {
	fmt.Println("Now validate the data for selling")

	chain := data["chain"]
	dex := data["dex"]

	tokenAddress := data["token_address"]
	if tokenAddress == "" || chain == "" || dex == "" {
		fmt.Println("err tokenAddress or chain or dex")
		return false
	}

	sellGasFee, err6 := strconv.ParseInt(data["sell_gas_fee"], 10, 64)
	if err6 != nil || sellGasFee <= 0 {
		fmt.Println("err sellGasFee")
		return false
	}

	startDatetime := data["start_datetime"]
	fmt.Println(startDatetime)

	duration, err8 := strconv.ParseInt(data["duration"], 10, 64)
	if err8 != nil || duration <= 0 {
		fmt.Println("err duration")
		return false
	}

	sellPassword := data["sell_password"]
	if base64.StdEncoding.EncodeToString([]byte(sellPassword)) != os.Getenv("SNIFF_PASSWORD") {
		fmt.Println("err sell_password")
		return false
	}

	sellTask, errT := NewApproveSellTask("Sell", chain, dex, tokenAddress, sellGasFee, duration)
	if errT != nil {
		log.Fatal(errT)
		return false
	}

	infoAdded, errA := client.Enqueue(sellTask, asynq.Timeout(24*time.Hour))
	if startDatetime != "" {
		myDate, err := time.Parse("2006-01-02T15:04", startDatetime)
		if err == nil {
			fmt.Println("willProcess at")
			fmt.Println(myDate)
			infoAdded, errA = client.Enqueue(sellTask, asynq.ProcessAt(myDate), asynq.Timeout(24*time.Hour))
		}
	}
	if errA != nil {
		log.Fatal(errA)
		return false
	}

	log.Printf(" [*] Successfully enqueued task: %+v", infoAdded)

	utils.SaveToRedis("approve_sell:"+tokenAddress, sellTask.Payload())
	return true
}

func ExecuteApproveSpending(data map[string]string) bool {
	fmt.Println("Now validate the data")

	chain := data["chain"]
	dex := data["dex"]

	tokenAddress := data["token_address"]
	if tokenAddress == "" || chain == "" || dex == "" {
		fmt.Println("err tokenAddress or chain or dex")
		return false
	}

	sellPassword := data["sell_password"]
	if base64.StdEncoding.EncodeToString([]byte(sellPassword)) != os.Getenv("SNIFF_PASSWORD") {
		fmt.Println("err sell_password")
		return false
	}

	fmt.Println(tokenAddress)
	fmt.Println(sellPassword)

	approveTask, errT := NewApproveSellTask("Approve", chain, dex, tokenAddress, 5, 0)
	if errT != nil {
		log.Fatal(errT)
		return false
	}

	infoAdded, errA := client.Enqueue(approveTask, asynq.Timeout(24*time.Hour))
	if errA != nil {
		log.Fatal(errA)
		return false
	}
	log.Printf(" [*] Successfully enqueued task: %+v", infoAdded)

	utils.SaveToRedis("approve_sell:"+tokenAddress, approveTask.Payload())
	return true
}

func NewApproveSellTask(ttype string, chain, dex, tokenAddress string, sellGasFee int64, duration int64) (*asynq.Task, error) {
	payload, err := json.Marshal(
		approveSellPayload{
			Chain:         chain,
			Dex:           dex,
			TokenAddress:  tokenAddress,
			ApproveStatus: "",
			ApproveTx:     "",
			SellGasFee:    sellGasFee,
			Duration:      duration,
			SellStatus:    "",
			SellTx:        "",
		})
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(ttype, "Approve") {
		return asynq.NewTask(TypeApprove, payload, asynq.MaxRetry(0)), nil
	} else {
		return asynq.NewTask(TypeSell, payload, asynq.MaxRetry(0)), nil
	}
}

type approveSellPayload struct {
	Chain        string
	Dex          string
	TokenAddress string

	// for tracking in db
	ApproveStatus string
	ApproveTx     string
	SellGasFee    int64
	Duration      int64
	SellStatus    string
	SellTx        string
}

func HandleApproveTask(ctx context.Context, t *asynq.Task) error {
	fmt.Println("On HandleApproveTask")
	var p approveSellPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		fmt.Println(err)
		return err
	}

	PerformApproving(p)
	return nil
}

func PerformApproving(pload approveSellPayload) {
	fmt.Println("On PerformApproving")
	pload.ApproveStatus = "Approving..."
	saveApproveStatusToRedis(pload)

	ethClient, err := ethclient.Dial(os.Getenv("WSS"))
	if err != nil {
		log.Fatal(err)
	}

	initNoneAndChain(ethClient, pload.Dex)

	router_add := os.Getenv("PROUTER_ADDRESS")
	if strings.EqualFold(pload.Dex, "APESWAP") {
		router_add = os.Getenv("AROUTER_ADDRESS")
	}
	fmt.Println(router_add)
	router_address = common.HexToAddress(router_add)

	tokenAddress := common.HexToAddress(pload.TokenAddress)
	tokenInstance, err := token.NewToken(tokenAddress, ethClient)
	if err != nil {
		log.Fatal(err)
	}

	allowance := getAccountAllowance(tokenInstance, router_address)
	if allowance.Cmp(big.NewInt(0)) > 0 {
		fmt.Println("Already Approved")
		pload.ApproveStatus = "Already Approved"
		saveApproveStatusToRedis(pload)
	} else {
		fmt.Println("Execute approving")
		pload.ApproveStatus = "Submmited"
		performApproving(ethClient, tokenInstance, pload, router_add)
	}
}

func saveApproveStatusToRedis(pload approveSellPayload) {
	mar, errMar := json.Marshal(pload)
	if errMar != nil {
		fmt.Println("Error in json.Marshal")
		fmt.Println(errMar)
	} else {
		utils.SaveToRedis("approve_sell:"+pload.TokenAddress, mar)
	}
}

func performApproving(ethClient *ethclient.Client, tokenInstance *token.Token, pload approveSellPayload, router_add string) {
	unlimit, ok := new(big.Int).SetString(consts.UNLIMITED, 10)
	if !ok {
		fmt.Println("approveSpendingToken err")
		return
	}
	opts := buildCustomDataToSend(ethClient, 5, big.NewInt(0))
	tx, err := tokenInstance.Approve(opts, common.HexToAddress(router_add), unlimit)
	if err != nil {
		fmt.Println("Error send approve 2")
		fmt.Println(os.Getenv("WSS"))
		fmt.Println(common.HexToAddress(router_add))
		fmt.Println(unlimit)
		fmt.Println(opts)
		log.Fatal(err)
		return
	}
	fmt.Printf("Sent approving, tx sent: %s\n", tx.Hash().Hex())
	pload.ApproveStatus = "SentTx"
	pload.ApproveTx = os.Getenv("SCAN_URL") + tx.Hash().Hex()
	saveApproveStatusToRedis(pload)
}

func GetApproveSellTasks() []map[string]any {
	data := utils.ReadMulti("approve_sell:*")
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

func buildCustomDataToSend(ethClient *ethclient.Client, gas int64, value *big.Int) *bind.TransactOpts {
	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, chainId)
	if err != nil {
		log.Fatal("Error init auth")
		log.Fatal(err)
	}
	nonce, err := ethClient.PendingNonceAt(context.Background(), walletAddress)
	if err != nil {
		log.Fatal(err)
	}

	auth.Nonce = big.NewInt(int64(nonce))
	fmt.Println("Nonce")
	fmt.Println(auth.Nonce)
	auth.Value = value                           // in wei
	auth.GasLimit = uint64(300000)               // in units
	auth.GasPrice = big.NewInt(gas * 1000000000) //big.NewInt(gas)

	return auth
}
