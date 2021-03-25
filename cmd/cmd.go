package cmd

import (
  "context"
  "crypto/ecdsa"
  "ethbench/ethereum"
  "fmt"
  "github.com/ethereum/go-ethereum/common"
  "github.com/ethereum/go-ethereum/common/hexutil"
  "github.com/ethereum/go-ethereum/core/types"
  "github.com/ethereum/go-ethereum/crypto"
  "github.com/ethereum/go-ethereum/ethclient"
  "github.com/spf13/cobra"
  "golang.org/x/crypto/sha3"
  "log"
  "math"
  "math/big"
  "os"
  "strconv"
  "sync"
  "sync/atomic"
  "time"
)

var parentAddress string
var parentAddressPrivateKey string

type CreatedAccounts struct {
  address string
  privateKey string
}

var continuous = &cobra.Command{
  Use:   "continuous",
  Short: "Run load test with cleanup and funding",
  Long:  `this command benchmarks eth transactions`,
  Run: func(cmd *cobra.Command, args []string) {
    runContinuousTest()
  },
}

var nonces map[string]uint64
var nonceLock sync.RWMutex

func init() {
  rootCmd.AddCommand(continuous)
  nonces = make(map[string]uint64)
}

var stop = false

func runContinuousTest() {
  var err error
  var nodes, numOfAccounts int
  nodes, err = strconv.Atoi(os.Getenv("NODES"))
  if err != nil {
    log.Fatal(err)
  }

  numOfAccounts, err = strconv.Atoi(os.Getenv("ACCOUNTS"))
  if err != nil {
    log.Fatal(err)
  }

  parentAddress = os.Getenv("PARENT_ADDRESS")
  parentAddressPrivateKey = os.Getenv("PARENT_PRIVATE_KEY")

  fmt.Printf("Parent Address %s\n", parentAddress)


  var testAccounts [][]CreatedAccounts

  for i := 1; i <= nodes; i++ {
    clientNode, err := ethclient.Dial(os.Getenv(fmt.Sprintf("%s%d", "RPC_URL", i)))
    if err != nil {
      log.Fatal(err)
    } else {
      if i == 1 {
        testAccounts = make([][]CreatedAccounts, nodes)
        for z := 0; z < nodes; z++ {
          testAccounts[z] = createAccounts(numOfAccounts)
          fundAccounts(testAccounts[z], clientNode)
        }
      }

      go func(node int, clientNode *ethclient.Client) {

        time.Sleep(time.Duration(2 * (node-1)) * time.Second)
        var flip = false
        for {
          go func(numOfAccounts int, clientNode *ethclient.Client, testAccounts []CreatedAccounts) {
            runOnce(testAccounts, clientNode, flip)
          }(numOfAccounts, clientNode, testAccounts[node-1])

          time.Sleep(time.Duration(2 * nodes) * time.Second)


          flip = !flip
          if stop {
            // for i := 0; i < sets; i++ {
            //  cleanup(testAccounts[((node-1) * sets) + i], clientNode)
            // }
            break
          }
        }
      }(i, clientNode)
    }
  }

  var text string
  stopString := "stop"
  for {
    fmt.Print("Enter stop to end test: ")
    fmt.Scanln(&text)
    if text == stopString {
      stop = true
      time.Sleep(1 * time.Minute)
      break
    }
  }
}

func runOnce(testAccounts []CreatedAccounts, client1 *ethclient.Client, flip bool) {
  for i := 0; i < len(testAccounts) - 1; i++ {
    go func(idx int) {
      //fmt.Printf("starting index %d\n", idx)
      if flip {
        err := sendEthToAddress(client1, testAccounts[idx + 1].address, "10000", testAccounts[idx].privateKey)
        if err != nil {
          fmt.Println(err)
        }
      } else {
        err := sendEthToAddress(client1, testAccounts[idx].address, "10000", testAccounts[idx+1].privateKey)
        if err != nil {
          fmt.Println(err)
        }
      }

    }(i)
  }
}

func fundAccounts(testAccounts []CreatedAccounts, client1 *ethclient.Client) {
  balance, err := ethereum.GetWeiBalance(parentAddress, client1)
  if err != nil {
    log.Fatal(err)
  }
  fbalance := new(big.Float)
  fbalance.SetString(balance.String())
  ethValue := new(big.Float).Quo(fbalance, big.NewFloat(math.Pow10(18)))
  fmt.Printf("Parent Account %s has %s ETH\n", parentAddress, ethValue.String())
  if ethValue.String() == "0" {
    log.Fatal("please fund the parent account with lots of eth")
  }

  for i := 0; i < len(testAccounts); i++ {
    tAddress := testAccounts[i]

    fmt.Printf("Current Index %d / %d\n", i + 1, len(testAccounts))
    err := sendEthToAddress(client1, tAddress.address, "1000000000000000000", parentAddressPrivateKey) // 10 ETH
    if err != nil {
      log.Fatal(err)
    }
  }
  fmt.Println("Funding: waiting 5 seconds for the addresses to fund/settle")
  time.Sleep(5 * time.Second)
  fmt.Println("Funding: verifying balances")


  // verifying the balances
  for i := 0; i < len(testAccounts); i++ {
    tAddress := testAccounts[i]
    balance, err := ethereum.GetWeiBalance(tAddress.address, client1)
    if err != nil {
      log.Fatal(err)
    }
    fbalance := new(big.Float)
    fbalance.SetString(balance.String())
    ethValue := new(big.Float).Quo(fbalance, big.NewFloat(math.Pow10(18)))
    fmt.Printf("Account %s has %s ETH\n", tAddress.address, ethValue.String())
    if ethValue.String() == "0" {
      fmt.Println("this should never happen but we got an address with 0 ETH! re-funding it now")
      err := sendEthToAddress(client1, tAddress.address, "100000000000000000000", parentAddressPrivateKey)
      if err != nil {
        log.Fatal(err)
      }
    }
  }
}

func cleanup(testAccounts []CreatedAccounts, client1 *ethclient.Client) {
  balance, err := ethereum.GetWeiBalance(parentAddress, client1)
  if err != nil {
    log.Fatal(err)
  }
  fbalance := new(big.Float)
  fbalance.SetString(balance.String())
  ethValue := new(big.Float).Quo(fbalance, big.NewFloat(math.Pow10(18)))
  fmt.Printf("Parent Account %s has %s ETH\n", parentAddress, ethValue.String())

  for k, tAddress := range testAccounts {
    // 1000000000000000000000 = 1000 ETH
    fmt.Printf("Current Index %d / %d\n", k, len(testAccounts))

    balance, err := ethereum.GetWeiBalance(tAddress.address, client1)
    if err != nil {
      log.Fatal(err)
    }
    fmt.Printf("Balance of account %s is %s\n", tAddress.address, balance.String())

    if balance.String() != "0" {

      err = sendEthToAddress(client1, parentAddress, balance.Sub(balance, big.NewInt(1000000000000000)).String(), tAddress.privateKey)
      if err != nil {
        log.Println(err)
      }

    } else {
      fmt.Printf("account %s has 0 balance", tAddress.address)
    }
  }

  // verifying the balances
  for _, tAddress := range testAccounts {
    balance, err := ethereum.GetWeiBalance(tAddress.address, client1)
    if err != nil {
      log.Fatal(err)
    }
    fbalance := new(big.Float)
    fbalance.SetString(balance.String())
    ethValue := new(big.Float).Quo(fbalance, big.NewFloat(math.Pow10(18)))
    fmt.Printf("Account %s has %s ETH\n", tAddress.address, ethValue.String())
  }
}

func createAccounts(count int) []CreatedAccounts {

  var testAccounts = make([]CreatedAccounts, count)
  for j := 0; j < count; j++ {
    privateKey,_ := crypto.GenerateKey()

    privateKeyBytes := crypto.FromECDSA(privateKey)
    //fmt.Printf("Private key: %s\n",hexutil.Encode(privateKeyBytes)[2:])

    publicKey := privateKey.Public()
    publicKeyECDSA, _ := publicKey.(*ecdsa.PublicKey)


    publicKeyBytes := crypto.FromECDSAPub(publicKeyECDSA)
    //fmt.Printf("Public key:\t %s\n",hexutil.Encode(publicKeyBytes)[4:])

    address := crypto.PubkeyToAddress(*publicKeyECDSA).Hex()
    //fmt.Printf("Public address (from ECDSA): \t%s\n",address)

    hash := sha3.NewLegacyKeccak256()
    hash.Write(publicKeyBytes[1:])
    //fmt.Printf("Public address (Hash of public key):\t%s\n",hexutil.Encode(hash.Sum(nil)[12:]))

    testAccounts[j].address = address
    testAccounts[j].privateKey = fmt.Sprintf("%s%s", "0x", hexutil.Encode(privateKeyBytes)[2:])
  }

  return testAccounts
}

func sendEthToAddress(client *ethclient.Client, toAddress string, amountInWei string, senderPrivateKey string) error {

  privateKey, err := crypto.HexToECDSA(senderPrivateKey[2:])
  if err != nil {
    return err
  }

  publicKey := privateKey.Public()
  publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
  if !ok {
    return fmt.Errorf("can't cast to ecds.PublicKey")
  }

  fromAddress := crypto.PubkeyToAddress(*publicKeyECDSA)

  var nonce uint64
  //nonce, err := client.PendingNonceAt(context.Background(), fromAddress)
  //if err != nil {
  //  return err
  //}
  nonce, err = getNonce(client, fromAddress)
  if err != nil {
    return err
  }

  value := new(big.Int)
  value, ok = value.SetString(amountInWei, 10)
  if !ok {
    return fmt.Errorf("wrong wei amount")
  }

  gasLimit := uint64(55723) // in units
  gasPriceWei, _ := strconv.Atoi(os.Getenv("GAS_PRICE_WEI"))
  gasPrice := big.NewInt(int64(gasPriceWei))

  var data []byte //nil
  tx := types.NewTransaction(nonce, common.HexToAddress(toAddress), value, gasLimit, gasPrice, data)

  chainID, _ := strconv.Atoi(os.Getenv("CHAIN_ID"))
  signedTx, err := types.SignTx(tx, types.NewEIP155Signer(big.NewInt(int64(chainID))), privateKey)
  if err != nil {
    return err
  }

  err = client.SendTransaction(context.Background(), signedTx)
  if err != nil {
    setNonce(client, fromAddress)
    return fmt.Errorf("failed to send eth: %s", err)
  }

  log.Printf("sent %s wei from %s to %s -> %s\n", amountInWei, fromAddress.String(), toAddress, tx.Hash().String())
  // time.Sleep(500)
  return nil
}

func getNonce(client *ethclient.Client, fromAddress common.Address) (uint64, error) {
  nonceLock.Lock()
  var nonce = nonces[fromAddress.Hex()]
  if nonce == 0 {
    nonce, err := client.PendingNonceAt(context.Background(), fromAddress)
    if err != nil {
      nonceLock.Unlock()
      return 0,err
    }
    nonces[fromAddress.Hex()] = nonce
    nonceLock.Unlock()
    return nonce, nil
  } else {
    atomic.AddUint64(&nonce, uint64(1))
    nonces[fromAddress.Hex()] = nonce
    nonceLock.Unlock()
    return nonce, nil
  }
}

func setNonce(client *ethclient.Client, fromAddress common.Address) error {
  nonceLock.Lock()
  nonce, err := client.PendingNonceAt(context.Background(), fromAddress)
  if err != nil {
    nonceLock.Unlock()
    return err
  }
  nonces[fromAddress.Hex()] = nonce
  return nil
}
