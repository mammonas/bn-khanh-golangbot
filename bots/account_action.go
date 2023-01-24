package bots

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"os"
	consts "panicbot/constants"
	"strings"
	"time"

	token "panicbot/constants/contracts_erc20"
	pancake "panicbot/constants/contracts_pancake_router"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Set up account
var walletAddress = setupAccount()
var privateKey = setUpPrivateKey()
var nextNonce = uint64(0)
var chainId = big.NewInt(56)
var router_address = common.HexToAddress(os.Getenv("PROUTER_ADDRESS"))
var sleepTime = time.Duration(33) * time.Millisecond // quicknode rare limit

// token to test: 0xd09Ff0c217D6410A77879F0896d68C8984797a86

func encrypt(key, text []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	b := base64.StdEncoding.EncodeToString(text)
	ciphertext := make([]byte, aes.BlockSize+len(b))
	iv := ciphertext[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}
	cfb := cipher.NewCFBEncrypter(block, iv)
	cfb.XORKeyStream(ciphertext[aes.BlockSize:], []byte(b))
	return ciphertext, nil
}

func decrypt(key, text []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(text) < aes.BlockSize {
		return nil, errors.New("ciphertext too short")
	}
	iv := text[:aes.BlockSize]
	text = text[aes.BlockSize:]
	cfb := cipher.NewCFBDecrypter(block, iv)
	cfb.XORKeyStream(text, text)
	data, err := base64.StdEncoding.DecodeString(string(text))
	if err != nil {
		return nil, err
	}
	return data, nil
}

func setUpPrivateKey() *ecdsa.PrivateKey {
	key := []byte("test test test test tested tests") // 32 bytes
	decodedCipher, err := base64.StdEncoding.DecodeString(os.Getenv("PRIVATE_KEY_META"))
	if err != nil {
		log.Fatal("PrivateKey Error")
	}
	result, err := decrypt(key, decodedCipher)
	if err != nil {
		log.Fatal(err)
	}
	prv, err := crypto.HexToECDSA(string(result))
	if err != nil {
		fmt.Println("Error init privateKey")
		log.Fatal(err)
	}
	return prv
}

func setupAccount() common.Address {

	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		log.Fatal("cannot assert type: publicKey is not of type *ecdsa.PublicKey")
	}

	fromAddress := crypto.PubkeyToAddress(*publicKeyECDSA)
	fmt.Println("Account address")
	fmt.Println(fromAddress)
	return fromAddress
}

func initNoneAndChain(client *ethclient.Client, dex string) {
	nonce, err := client.PendingNonceAt(context.Background(), walletAddress)
	if err != nil {
		log.Fatal(err)
	}
	nextNonce = nonce
	fmt.Println("nextNonce")
	fmt.Println(nextNonce)
	cID, err := client.ChainID(context.Background())
	if err != nil {
		log.Fatal(cID)
	}
	chainId = cID
	fmt.Println("chainId")
	fmt.Println(chainId)

	if strings.EqualFold(dex, "APESWAP") {
		router_address = common.HexToAddress(os.Getenv("AROUTER_ADDRESS"))
	}
}

func ExecuteSniffBuyingThenSelling(client *ethclient.Client, pload sniffLiquidationPayload, routerInstance *pancake.Pancake, tokenInstance *token.Token, buyToken string, stableToken string, priceBNB *big.Int, priceAtLiquid *big.Float, tokenDecimal uint8) {
	fmt.Println("At here")
	fmt.Println(router_address)

	// Execute Buying, save tx_id
	executeBuyingToken(client, routerInstance, tokenInstance, pload, buyToken, stableToken, priceBNB, priceAtLiquid, tokenDecimal)
	// after Buying: Execute loop to check AccountBalance
	// if AccountBalance > 0, execute loop to check AccountAllowance
	// if AccountAllowance <= AccountBalance, execute ApproveSpending then continue loop to check AccountAllowance
	// if AccountAllowance >= AccountBalance, execute loop for PriceCheck
	// if PriceCheck > BuyAmount * 50%, execute Sell, save tx_id

}

func executeBuyingToken(client *ethclient.Client, routerInstance *pancake.Pancake, tokenInstance *token.Token, pload sniffLiquidationPayload, buyToken string, stableToken string, priceBNB *big.Int, priceAtLiquid *big.Float, tokenDecimal uint8) {
	startBuyingTime := time.Now()
	value := big.NewInt(0)
	isBNB := false
	if strings.EqualFold(stableToken, os.Getenv("BNB")) {
		isBNB = true
		bnbToBuy := new(big.Float).Quo(big.NewFloat(pload.BuyAmount), new(big.Float).SetInt(priceBNB))
		fmt.Println("bnbToBuy")
		fmt.Println(bnbToBuy)
		bnbInFloat := new(big.Float).Mul(bnbToBuy, big.NewFloat(1000000000000000000))
		bnbInFloat.Int(value)
		fmt.Println(value)
	}

	opts := buildDataToSend(pload.BuyGasFee, value) // need to optimize it
	amountMinOut := calculateAmountMinOut(pload, priceAtLiquid, tokenDecimal)

	path := []common.Address{
		common.HexToAddress(stableToken),
		common.HexToAddress(buyToken),
	}
	pathReserved := []common.Address{
		common.HexToAddress(buyToken),
		common.HexToAddress(stableToken),
	}
	deadline := big.NewInt(time.Now().UTC().Unix() + 1000000)

	if isBNB {
		tx, err := routerInstance.SwapExactETHForTokensSupportingFeeOnTransferTokens(opts, amountMinOut, path, walletAddress, deadline)
		pload.ProcessStatus = "bought:waitbalance"
		if err != nil {
			fmt.Println("Errr sending transaction ETH")
			fmt.Println(err)
			pload.ProcessStatus = "buy:failedsendtx"
		}
		elapsed := time.Since(startBuyingTime)
		log.Printf("Took %s to send BNB Trans", elapsed)
		fmt.Printf("buy in BNB, tx sent: %s\n", tx.Hash().Hex())
		pload.BuyStatus = "observing"
		pload.BuyTx = os.Getenv("SCAN_URL") + tx.Hash().Hex()
		saveStatusToRedis(pload)
		nextNonce += 1
		observeAccountAmountThenSell(routerInstance, tokenInstance, pload, value, pathReserved)
	} else {
		amountIn := calculateAmountIn(pload)
		elapsed := time.Since(startBuyingTime)
		log.Printf("Took %s to send normal Trans", elapsed)
		tx, err := routerInstance.SwapExactTokensForTokensSupportingFeeOnTransferTokens(opts, amountIn, amountMinOut, path, walletAddress, deadline)
		pload.ProcessStatus = "bought:waitbalance"
		if err != nil {
			fmt.Println("Errr sending transaction normal")
			fmt.Println(err)
			pload.ProcessStatus = "buy:failedsendtx"
		}
		fmt.Printf("buy in StableCoin, tx sent: %s\n", tx.Hash().Hex())
		pload.BuyStatus = "observing"
		pload.BuyTx = os.Getenv("SCAN_URL") + tx.Hash().Hex()
		saveStatusToRedis(pload)
		nextNonce += 1
		observeAccountAmountThenSell(routerInstance, tokenInstance, pload, amountIn, pathReserved)
	}
}

func observeAccountAmountThenSell(routerInstance *pancake.Pancake, tokenInstance *token.Token, pload sniffLiquidationPayload, buyAmount *big.Int, pathReserved []common.Address) {
	fmt.Println("At here 1,5")
	fmt.Println(router_address)
	inLoop := true
	found := false
	balance := big.NewInt(0)
	zero := big.NewInt(0)
	startTime := time.Now().UTC()
	expireTime := startTime.Add(time.Duration(2) * time.Minute)
	for inLoop {
		balance = getAccountBalance(tokenInstance)
		fmt.Println("Observe Balance")
		fmt.Println(balance)
		if balance.Cmp(zero) == 1 {
			inLoop = false
			found = true
			fmt.Println("Found Balance")
		}
		if time.Now().UTC().After(expireTime) {
			inLoop = false
			found = false
			fmt.Println("Not Found Balance")
		}
		time.Sleep(sleepTime)
	}
	if found {
		pload.ProcessStatus = "balancegood:observeallowance"
		saveStatusToRedis(pload)
		observeAllowanceThenSell(routerInstance, tokenInstance, pload, balance, buyAmount, pathReserved)
		elapsed := time.Since(startTime)
		log.Printf("Took %s to get accountbalance", elapsed)
	} else {
		fmt.Println("2 minutes but cannot find balance > 0")
		pload.ProcessStatus = "cancel:balancenotgood"
		pload.BuyStatus = "failed"
		saveStatusToRedis(pload)
	}
}

func observeAllowanceThenSell(routerInstance *pancake.Pancake, tokenInstance *token.Token, pload sniffLiquidationPayload, balance *big.Int, buyAmount *big.Int, pathReserved []common.Address) {
	fmt.Println("At here 2")
	fmt.Println(router_address)
	inLoop := true
	found := false
	startTime := time.Now().UTC()
	expireTime := startTime.Add(time.Duration(2) * time.Minute)
	router_address = common.HexToAddress(os.Getenv("PROUTER_ADDRESS"))
	if strings.EqualFold(pload.Dex, "APESWAP") {
		router_address = common.HexToAddress(os.Getenv("AROUTER_ADDRESS"))
	}
	allowance := getAccountAllowance(tokenInstance, router_address)

	if allowance.Cmp(balance) < 0 {
		fmt.Println("gui lenh approve")
		isSentApprove := approveSpendingToken(tokenInstance, pload)
		for inLoop && isSentApprove {
			allowance = getAccountAllowance(tokenInstance, router_address)
			if allowance.Cmp(balance) >= 0 {
				inLoop = false
				found = true
			}
			if time.Now().UTC().After(expireTime) {
				fmt.Println("Expired on allowance")
				inLoop = false
				found = false
			}
			time.Sleep(sleepTime)
		}
	} else {
		fmt.Println("observePriceThenSell1")
		pload.ProcessStatus = "allowance:good:observeprice"
		saveStatusToRedis(pload)
		observePriceThenSell(routerInstance, pload, buyAmount, balance, pathReserved)
		elapsed := time.Since(startTime)
		log.Printf("Took %s to getallowance 1", elapsed)
	}
	if found {
		fmt.Println("observePriceThenSell2")
		pload.ProcessStatus = "allowance:good:observeprice"
		saveStatusToRedis(pload)
		observePriceThenSell(routerInstance, pload, buyAmount, balance, pathReserved)
		elapsed := time.Since(startTime)
		log.Printf("Took %s to getallowance 2", elapsed)
	} else {
		pload.ProcessStatus = "cancel:notallowance"
		pload.SellStatus = "failed"
		saveStatusToRedis(pload)
	}
}

func observePriceThenSell(routerInstance *pancake.Pancake, pload sniffLiquidationPayload, buyAmount *big.Int, balance *big.Int, pathReserved []common.Address) {
	willSell := false
	inLoop := true
	startTime := time.Now().UTC()
	expireTime := startTime.Add(time.Duration(5) * time.Minute)
	expectAmount := big.NewInt(0)
	for inLoop {
		amountToReceive, err := routerInstance.GetAmountsOut(&bind.CallOpts{}, balance, pathReserved)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("amountToReceive")
		fmt.Println(amountToReceive)
		expectAmount = new(big.Int).Mul(buyAmount, big.NewInt(2))
		if amountToReceive[1].Cmp(expectAmount) >= 0 { // should 0 :D -2: cheat
			willSell = true
			inLoop = false
		}
		if time.Now().UTC().After(expireTime) {
			inLoop = false
		}
		time.Sleep(sleepTime)
	}
	if willSell {
		fmt.Println("SELLLLL")
		executeSellingToken(routerInstance, pload, pathReserved, balance, expectAmount)
	} else {
		pload.ProcessStatus = "notsell:pricenotgood"
		pload.SellStatus = "pending"
		saveStatusToRedis(pload)
	}
}

func executeSellingToken(routerInstance *pancake.Pancake, pload sniffLiquidationPayload, pathReserved []common.Address, balance *big.Int, expectAmount *big.Int) {
	// expectAmountWithTemp := new(big.Int).Mul(expectAmount, big.NewInt(100-pload.SellSlippage))
	// expectAmountWithSlippage := new(big.Int).Quo(expectAmountWithTemp, big.NewInt(100))
	expectAmountWithSlippage := big.NewInt(0)
	opts := buildDataToSend(pload.SellGasFee, big.NewInt(0))
	deadline := big.NewInt(time.Now().UTC().Unix() + 1000000)
	if strings.EqualFold(pathReserved[1].String(), os.Getenv("BNB")) {
		fmt.Println("Sell to BNB")
		tx, err := routerInstance.SwapExactTokensForETHSupportingFeeOnTransferTokens(opts, balance, expectAmountWithSlippage, pathReserved, walletAddress, deadline)
		pload.ProcessStatus = "sell:senttx"
		if err != nil {
			fmt.Println("Error sending ETH Trans to sell")
			fmt.Println(err)
			pload.ProcessStatus = "sell:failedsenttx"
		}
		pload.SellTx = os.Getenv("SCAN_URL") + tx.Hash().Hex()
		saveStatusToRedis(pload)
	} else {
		fmt.Println("Sell to StableCoin")
		tx, err := routerInstance.SwapExactTokensForTokensSupportingFeeOnTransferTokens(opts, balance, expectAmountWithSlippage, pathReserved, walletAddress, deadline)
		pload.ProcessStatus = "sell:senttx"
		if err != nil {
			fmt.Println("Error sending Stable Trans to sell")
			fmt.Println(err)
			pload.ProcessStatus = "sell:failedsenttx"
		}
		pload.SellTx = os.Getenv("SCAN_URL") + tx.Hash().Hex()
		saveStatusToRedis(pload)
	}
}

func approveSpendingToken(tokenInstance *token.Token, pload sniffLiquidationPayload) bool {
	router_add := os.Getenv("PROUTER_ADDRESS")
	if strings.EqualFold(pload.Dex, "APESWAP") {
		router_add = os.Getenv("AROUTER_ADDRESS")
	}
	unlimit, ok := new(big.Int).SetString(consts.UNLIMITED, 10)
	if !ok {
		fmt.Println("approveSpendingToken err")
		return false
	}
	opts := buildDataToSend(5, big.NewInt(0))
	tx, err := tokenInstance.Approve(opts, common.HexToAddress(router_add), unlimit)
	if err != nil {
		fmt.Println("Error send approve")
		log.Fatal(err)
		return false
	}
	nextNonce += 1
	fmt.Printf("Sent approving, tx sent: %s\n", tx.Hash().Hex())
	return true
}

func buildDataToSend(gas int64, value *big.Int) *bind.TransactOpts {
	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, chainId)
	if err != nil {
		log.Fatal("Error init auth")
		log.Fatal(err)
	}

	auth.Nonce = big.NewInt(int64(nextNonce))
	fmt.Println("Nonce")
	fmt.Println(auth.Nonce)
	auth.Value = value                           // in wei
	auth.GasLimit = uint64(300000)               // in units
	auth.GasPrice = big.NewInt(gas * 1000000000) //big.NewInt(gas)

	return auth
}

func calculateAmountMinOut(pload sniffLiquidationPayload, priceAtLiquid *big.Float, tokenDecimal uint8) *big.Int {
	expectAmount := new(big.Float).Quo(big.NewFloat(pload.BuyAmount), priceAtLiquid)
	fmt.Println("expectAmount")
	fmt.Println(expectAmount)
	expectAmountWithSlippage1 := new(big.Float).Mul(expectAmount, big.NewFloat(float64(100-pload.BuySlippage)))
	expectAmountWithSlippage2 := new(big.Float).Quo(expectAmountWithSlippage1, big.NewFloat(100))
	fmt.Println("expectAmountWithSlippage2")
	fmt.Println(expectAmountWithSlippage2)
	base := big.NewInt(10)
	po := big.NewInt(int64(tokenDecimal))
	basePO := base.Exp(base, po, nil)
	amountMinOutFloat := new(big.Float).Mul(expectAmountWithSlippage2, new(big.Float).SetInt(basePO))
	amountMinOut := big.NewInt(0)
	amountMinOutFloat.Int(amountMinOut)

	fmt.Println("Expected amountMinOut")
	fmt.Println(amountMinOut)

	return amountMinOut
}

func calculateAmountIn(pload sniffLiquidationPayload) *big.Int {
	base := big.NewInt(10)
	po := big.NewInt(18)
	basePO := base.Exp(base, po, nil)
	amountInFloat := new(big.Float).Mul(big.NewFloat(pload.BuyAmount), new(big.Float).SetInt(basePO))
	amountIn := big.NewInt(0)
	amountInFloat.Int(amountIn)

	fmt.Println("Expected amountIn")
	fmt.Println(amountIn)
	return amountIn
}

func getAccountBalance(tokenInstance *token.Token) *big.Int {
	balance, err := tokenInstance.BalanceOf(&bind.CallOpts{}, walletAddress)
	if err != nil {
		fmt.Println("Error in get balance")
		fmt.Println(walletAddress)
		log.Fatal(err)
	}
	fmt.Println(walletAddress)
	return balance
}

func getAccountAllowance(tokenInstance *token.Token, raddress common.Address) *big.Int {
	allowance, err := tokenInstance.Allowance(&bind.CallOpts{}, walletAddress, raddress)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("walletAddress")
	fmt.Println(walletAddress)
	fmt.Println("raddress")
	fmt.Println(raddress)
	fmt.Println("allowance")
	fmt.Println(allowance)
	return allowance
}
