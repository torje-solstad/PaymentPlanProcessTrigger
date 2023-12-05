package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/sns"
	_ "github.com/denisenkom/go-mssqldb"
)

var (
	connectionError                            error            = nil
	sess                                       *session.Session = nil
	DWH_CONSTR_DYNAMIC                         string           = os.Getenv("DWH_CONSTR_DYNAMIC")
	DWH_USERNAME                               string           = os.Getenv("DWH_USERNAME")
	DWH_PASSWORD                               string           = os.Getenv("DWH_PASSWORD")
	DWH_DB                                     string           = os.Getenv("DWH_DB")
	BPE_ENDPOINT                               string           = os.Getenv("BPE_ENDPOINT")
	SNS_TOPIC_NAME                             string           = os.Getenv("SNS_TOPIC_NAME")
	BUCKET                                     string           = os.Getenv("BUCKET")
	FILE_NAME                                  string           = os.Getenv("FILE_NAME")
	INCLUDE_DAYS                               string           = os.Getenv("INCLUDE_DAYS")
	DUE_BY_NUMBER_OF_DAYS                      string           = os.Getenv("DUE_BY_NUMBER_OF_DAYS")
	FETCH_PAYMENTPLANS_WITHOUT_STARTINGPROCESS string           = os.Getenv("FETCH_PAYMENTPLANS_WITHOUT_STARTINGPROCESS")
	// FROM               string                    = os.Getenv("FROM")
	// TO                 string                    = os.Getenv("TO")
	DB *sql.DB = nil
)

type InputData struct {
}

func evauluateAndExtractReadOnlyConfig() bool {
	return strings.EqualFold(FETCH_PAYMENTPLANS_WITHOUT_STARTINGPROCESS, "true")

}

func uploadFile(data string) error {
	fmt.Printf("Upload file Recived -> %s", data)
	svc := s3.New(sess)
	f, err := os.Create(fmt.Sprintf("/tmp/%s", FILE_NAME))

	if err != nil {
		fmt.Println("Error opening file")
		fmt.Println(err)
		return err
	}
	defer f.Close()
	bytelen, err := f.WriteString(data)

	if err != nil {
		fmt.Println("Error writing to file")
		fmt.Println(err)
		return err
	}

	fmt.Printf("Wrote %d bytes to file \n", bytelen)

	fmt.Println("Resetting file before upload")

	f.Seek(0, io.SeekStart)
	op, err := svc.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(BUCKET),
		Body:   f,
		Key:    aws.String(FILE_NAME),
	})

	if err != nil {

		fmt.Println("Put object didnt work")
		fmt.Println(err)
		return err
	}
	fmt.Println(op.String())
	return nil

}

func getFileS3() (string, error) {
	fmt.Println("Trying to get a file")
	fmt.Println(fmt.Sprintf("FileName=%v", FILE_NAME))
	svc := s3.New(sess)
	f, err := svc.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(BUCKET),
		Key:    aws.String(FILE_NAME),
	})

	if err != nil {
		fmt.Println("Could not get file")
		fmt.Println(err)
		return "", err
	}

	bytes, e := ioutil.ReadAll(f.Body)

	if e != nil {
		fmt.Println("Could not read file")
		return "", e
	}

	return string(bytes), nil

}

func fetchLatestTimeStampCollectionAccount() (string, error) {
	var latestTimesStamp string
	rows, err := DB.Query("SELECT TOP 1 DW_TimeStamp FROM view_collectionaccount order by DW_TimeStamp desc")
	defer rows.Close()

	if err != nil {
		return "", err
	}
	next := rows.Next()
	fmt.Printf("Got next %v ", next)
	err = rows.Scan(&latestTimesStamp)
	if err != nil {

		return "", err
	}

	return latestTimesStamp, nil

}

func HandleRequest(ctx context.Context, inputData InputData) (string, error) {

	if connectionError != nil {
		fmt.Println("Could not connect to db")
		return "", connectionError
	}

	loc, _ := setLocationGlobal()

	timeStamp, _ := fetchLatestTimeStampCollectionAccount()

	fmt.Println(fmt.Sprintf("The latest update timestamp is: %s", timeStamp))
	t := parseDateTimeStamp(timeStamp, loc)
	fmt.Println(fmt.Sprintf("Parsed date is %v", t))

	fmt.Println(timeStamp)
	fmt.Println(time.Parse(time.RFC3339, timeStamp))

	timeWhithinlimit, dif := timeWithinLimitMinutes(t, 30)
	if !timeWhithinlimit {
		content := aws.String(fmt.Sprintf("Datawarehouse not updated. %d minutes since last update", dif))
		sendEmailNotification(content)
		return *content, nil
	}

	res, err := requestPaymentPlanProcess()
	fmt.Println("Done...Reponse: ")
	if err == nil {
		toDayStamp := formatDate(time.Now())
		err := uploadFile(toDayStamp)
		if err != nil {
			fmt.Println(fmt.Sprintf("Could not upload file %v", FILE_NAME))
			fmt.Print(fmt.Sprintf("Cause: %v", err.Error()))
		}
	}
	fmt.Println(res)

	sendEmailNotification(&res)
	return res, nil
}
func timeWithinLimitMinutes(dateTime time.Time, minutes int64) (bool, int64) {

	toDay := time.Now()
	fmt.Println(fmt.Sprintf("Today is %v", toDay))
	fmt.Println(fmt.Sprintf("Last updated is %v", dateTime))

	difference := toDay.UnixMilli() - dateTime.UnixMilli()
	minutesDiffernece := difference / 1000 / 60

	return !(minutesDiffernece > minutes), minutesDiffernece
}

func setLocationGlobal() (*time.Location, error) {
	loc, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		fmt.Println("Unable to load locale Europe/Oslo")
		return nil, err
	} else {
		time.Local = loc

	}
	return loc, nil
}
func parseDateTimeStamp(dateTime string, loc *time.Location) time.Time {
	parsed, _ := time.Parse(time.RFC3339, dateTime)
	return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), parsed.Hour(), parsed.Minute(), parsed.Second(), parsed.Nanosecond(), loc)
}
func main() {

	DB, connectionError = initDB()
	fmt.Println("Tried initializing db")
	if connectionError != nil {
		sendEmailNotification(aws.String(fmt.Sprintf("Could not open DB: %s", connectionError.Error())))
		panic(connectionError)
	}

	defer DB.Close()

	sess = session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))

	lambda.Start(HandleRequest)
}

func requestPaymentPlanProcess() (string, error) {

	content, err := getFileS3()
	if err != nil {
		fmt.Println("Error getting last run timestamp file")
	}

	fmt.Println(fmt.Sprintf("Last request = %v", content))
	alreadyRequestedToday, err := isTimestampToday(content, "2006-01-02")
	if alreadyRequestedToday {
		info := "Already requested due paymentspland today. No request made"
		fmt.Println(info)
		return info, err
	}

	var byteArr []byte
	// uniqueCases = []CollectionAccountId{}
	msg, err := callEndPoint(byteArr)
	if err != nil {
		fmt.Println("Could not send request!")
		return msg, err
	}
	finalMsg := msg
	// sendEmailNotification(&finalMsg)
	return finalMsg, nil
}

func isTimestampToday(timestamp string, layout string) (bool, error) {

	date, err := time.Parse(layout, timestamp)
	if err != nil {
		return false, err
	}

	fmt.Println(fmt.Printf("TIMESTAMP %v vs %v", timestamp, date))
	tdYear, tdMonth, tdDay := time.Now().Date()
	if tdYear == date.Year() && tdMonth == date.Month() && date.Day() == tdDay {
		return true, nil
	}
	return false, nil
}

func initDB() (*sql.DB, error) {
	constr := fmt.Sprintf(DWH_CONSTR_DYNAMIC, DWH_USERNAME, DWH_PASSWORD, DWH_DB)
	DB, err := sql.Open("mssql", constr)
	if err != nil {
		fmt.Println("Could not connect")
		return nil, err
	}
	return DB, nil
}

func formatDate(date time.Time) string {
	year := date.Year()
	month := date.Month()
	day := date.Day()

	formatDigit := func(n int) string {
		if n < 10 {
			return fmt.Sprintf("%d%d", 0, n)
		} else {
			return fmt.Sprintf("%d", n)
		}
	}

	return fmt.Sprintf("%v-%v-%v", year, formatDigit(int(month)), formatDigit(int(day)))
}

func callEndPoint(b []byte) (string, error) {

	extractInterval := func(interval string) int {
		n, _ := strconv.Atoi(interval)
		return int(math.Abs(float64(n)))
	}

	from := formatDate(time.Now().AddDate(0, 0, extractInterval(DUE_BY_NUMBER_OF_DAYS)*-1))
	fmt.Println(fmt.Sprintf("FROM = %s", from))

	to := formatDate(time.Now().AddDate(0, 0, 30))
	fmt.Println(fmt.Sprintf("TO = %s", to))
	url := BPE_ENDPOINT + fmt.Sprintf("from=%v&to=%v&partition=true&dataOnly=%v&partitionIncludedRange=%s", from, to, evauluateAndExtractReadOnlyConfig(), INCLUDE_DAYS)
	fmt.Println(fmt.Sprintf("URL IS = %s", url))
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		fmt.Println("Unable to set up request")
		fmt.Println(err)
		return fmt.Sprintf("Error creating request -> %v", err), nil
	}

	// req.Header.Add("Content-Type", "application/json")
	// fmt.Println(BPE_ENDPOINT)
	client := http.Client{}
	res, err := client.Do(req)

	if err != nil {
		fmt.Println("Error sending request...")
		fmt.Println(err)
	}

	if strings.Trim(strings.Split(res.Status, " ")[0], " ") != strconv.Itoa(200) {
		return fmt.Sprintf("Failed. Status = %s", res.Status), nil
	}
	resBytes, err := ioutil.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		return fmt.Sprintf("Error reading response - > %v", err), nil
	}

	return string(resBytes), err
}

func sendEmailNotification(msg *string) {
	fmt.Println("testing Topics...")

	svc := sns.New(sess)

	result, err := svc.ListTopics(nil)
	if err != nil {
		fmt.Println("No topics to list")
		panic(err)
	}
	topicName := getTopicName(SNS_TOPIC_NAME, result)

	fmt.Println(result.Topics)
	fmt.Println("Topicname -> " + *topicName)
	fmt.Println(fmt.Sprintf("Length -> %d", len(result.Topics)))
	output, err := svc.Publish(&sns.PublishInput{
		TopicArn: topicName,
		Message:  msg,
		Subject:  aws.String("BPE PaymentplanAgreement"),
	})
	if err != nil {
		fmt.Println("Could not publish notification...")
		fmt.Println(err)
	}

	fmt.Println(*output.MessageId)

}

func getTopicName(topicname string, topics *sns.ListTopicsOutput) *string {
	for _, topic := range topics.Topics {
		topicNameParts := strings.Split(*topic.TopicArn, ":")

		if topicNameParts[len(topicNameParts)-1] == topicname {
			return *&topic.TopicArn
		}
	}
	return nil
}
