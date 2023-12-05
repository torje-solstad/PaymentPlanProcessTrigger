# PaymentPlanProcessTrigger

## Description

A lambda trigger for initializing paymentplanprocess

## Table of Contents

- [Installation Windows](#installation windows)

## Installation Windows

Install as normal goland procject and build


```bash
# Download git steps
git clone https://github.com/your-username/your-project.git
cd your-project

# Example build and zip

SET GOOS=linux
go build .\src\main


# In order to use as an AWS lambda, download build-lambda-zip.exe
go install github.com/aws/aws-lambda-go/cmd/build-lambda-zip@latest

# zip the lambda function

%USERPROFILE%\Go\bin\build-lambda-zip.exe -o example.zip main

# Upload as zip file to aws. Remember to reference your zip file name in when configuring the lambda in AWS