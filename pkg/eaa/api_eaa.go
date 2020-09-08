// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2019 Intel Corporation

package eaa

import (
	"encoding/json"
	"net/http"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/gorilla/mux"
)

// DeregisterApplication implements https API
func DeregisterApplication(w http.ResponseWriter, r *http.Request) {
	eaaCtx := r.Context().Value(contextKey("appliance-ctx")).(*Context)

	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	clientCert := r.TLS.PeerCertificates[0]
	commonName := clientCert.Subject.CommonName
	URN, err := CommonNameStringToURN(commonName)
	if err != nil {
		log.Errf("Error during converting Common Name to URN: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Check preemptively if a Service exists to return the HTTP code that is more likely to be
	// correct
	statusCode := http.StatusNoContent
	if !isServicePresent(commonName, eaaCtx) {
		statusCode = http.StatusNotFound
	}

	// Prepare Service structure
	var serv Service
	serv.URN = &URN
	svcMsg := ServiceMsg{Svc: &serv, Action: serviceActionDeregister}

	// Create Watermill Message and publish it
	data, err := json.Marshal(svcMsg)
	if err != nil {
		log.Errf("Error during Service structure marshaling: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	msg := message.NewMessage(serv.URN.String(), data)

	err = eaaCtx.MsgBrokerCtx.publish(servicesTopic, servicesTopic, msg)
	if err != nil {
		log.Errf("Error during Message publishing: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(statusCode)

	log.Debugf("Successfully processed DeregisterApplication from %s",
		commonName)
}

// GetNotifications implements https API
func GetNotifications(w http.ResponseWriter, r *http.Request) {
	eaaCtx := r.Context().Value(contextKey("appliance-ctx")).(*Context)

	if eaaCtx.serviceInfo == nil {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(http.StatusInternalServerError)
	}

	statCode, err := createWsConn(w, r)
	if err != nil {
		log.Errf("Error in WebSocket Connection Creation: %#v", err)
		if statCode != 0 {
			w.Header().Set("Content-Type", "application/json; charset=UTF-8")
			w.WriteHeader(statCode)
		}
		return
	}

	log.Debugf("Successfully processed GetNotifications from %s",
		r.TLS.PeerCertificates[0].Subject.CommonName)
}

// GetServices implements https API
func GetServices(w http.ResponseWriter, r *http.Request) {
	var servList ServiceList
	eaaCtx := r.Context().Value(contextKey("appliance-ctx")).(*Context)

	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(http.StatusOK)

	if eaaCtx.serviceInfo == nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	for _, serv := range eaaCtx.serviceInfo {
		servList.Services = append(servList.Services, serv)
	}

	encoder := json.NewEncoder(w)
	err := encoder.Encode(servList)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	log.Debugf("Successfully processed GetServices from %s",
		r.TLS.PeerCertificates[0].Subject.CommonName)
}

// GetSubscriptions implements https API
func GetSubscriptions(w http.ResponseWriter, r *http.Request) {
	eaaCtx := r.Context().Value(contextKey("appliance-ctx")).(*Context)
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(http.StatusOK)

	var (
		subs       *SubscriptionList
		commonName string
		err        error
	)

	commonName = r.TLS.PeerCertificates[0].Subject.CommonName

	if subs, err = getConsumerSubscriptions(commonName, eaaCtx); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Errf("Consumer Subscription List Getter: %s",
			err.Error())
		return
	}

	if err = json.NewEncoder(w).Encode(*subs); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Errf("Consumer Subscription List Getter: %s",
			err.Error())
		return
	}

	log.Debugf("Successfully processed GetSubscriptions from %s", commonName)
}

// PushNotificationToSubscribers implements https API
func PushNotificationToSubscribers(w http.ResponseWriter, r *http.Request) {
	eaaCtx := r.Context().Value(contextKey("appliance-ctx")).(*Context)
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	var notif NotificationFromProducer

	commonName := r.TLS.PeerCertificates[0].Subject.CommonName

	err := json.NewDecoder(r.Body).Decode(&notif)
	if err != nil {
		log.Errf("Error in Publish Notification: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	statCode, err := sendNotificationToAllSubscribers(commonName, notif, eaaCtx)
	if err != nil {
		log.Errf("Error in Publish Notification: %s", err.Error())
	}

	w.WriteHeader(statCode)
	log.Debugf("Successfully processed PushNotificationToSubscribers from %s",
		commonName)
}

// RegisterApplication implements https API
func RegisterApplication(w http.ResponseWriter, r *http.Request) {
	var serv Service
	eaaCtx := r.Context().Value(contextKey("appliance-ctx")).(*Context)
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	clientCert := r.TLS.PeerCertificates[0]
	commonName := clientCert.Subject.CommonName

	err := json.NewDecoder(r.Body).Decode(&serv)
	if err != nil {
		log.Errf("Register Application: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Create URN from commonName
	var URN URN
	if URN, err = CommonNameStringToURN(commonName); err != nil {
		log.Errf("Error during URN generation: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	serv.URN = &URN

	// Prepare ServiceMsg that will published using a Message Broker
	svcMsg := ServiceMsg{Svc: &serv, Action: serviceActionRegister}

	// Create Watermill Message and publish it
	data, err := json.Marshal(svcMsg)
	if err != nil {
		log.Errf("Error during Service structure marshaling: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	msg := message.NewMessage(commonName, data)

	err = eaaCtx.MsgBrokerCtx.publish(servicesTopic, servicesTopic, msg)
	if err != nil {
		log.Errf("Error during Message publishing: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	log.Debugf("Successfully processed RegisterApplication from %s",
		commonName)
}

// SubscribeNamespaceNotifications implements https API
func SubscribeNamespaceNotifications(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	eaaCtx := r.Context().Value(contextKey("appliance-ctx")).(*Context)
	var (
		sub        []NotificationDescriptor
		commonName string
		err        error
		statCode   int
	)

	if err = json.NewDecoder(r.Body).Decode(&sub); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Errf("Namespace Notification Registration: %s",
			err.Error())
		return
	}

	commonName = r.TLS.PeerCertificates[0].Subject.CommonName

	vars := mux.Vars(r)

	statCode, err = addSubscriptionToNamespace(commonName,
		vars["urn.namespace"], sub, eaaCtx)

	if err != nil {
		log.Errf("Namespace Notification Registration: %s",
			err.Error())
	}

	w.WriteHeader(statCode)
	log.Debugf("Successfully processed SubscribeNamespaceNotifications from %s",
		commonName)
}

// SubscribeServiceNotifications implements https API
func SubscribeServiceNotifications(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	eaaCtx := r.Context().Value(contextKey("appliance-ctx")).(*Context)
	var (
		sub        []NotificationDescriptor
		commonName string
		err        error
		statCode   int
	)

	if err = json.NewDecoder(r.Body).Decode(&sub); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Errf("Service Notification Registration: %s", err.Error())
		return
	}

	commonName = r.TLS.PeerCertificates[0].Subject.CommonName

	vars := mux.Vars(r)

	statCode, err = addSubscriptionToService(commonName,
		vars["urn.namespace"], vars["urn.id"], sub, eaaCtx)

	if err != nil {
		log.Errf("Service Notification Registration: %s", err.Error())
	}

	w.WriteHeader(statCode)
	log.Debugf("Successfully processed SubscribeServiceNotifications from %s",
		commonName)
}

// UnsubscribeAllNotifications implements https API
func UnsubscribeAllNotifications(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	eaaCtx := r.Context().Value(contextKey("appliance-ctx")).(*Context)
	commonName := r.TLS.PeerCertificates[0].Subject.CommonName
	statCode, err := removeAllSubscriptions(commonName, eaaCtx)
	if err != nil {
		log.Errf("Error in UnsubscribeAllNotifications: %s", err.Error())
	}

	w.WriteHeader(statCode)
	log.Debugf("Successfully processed UnsubscribeAllNotifications from %s",
		commonName)
}

// UnsubscribeNamespaceNotifications implements https API
func UnsubscribeNamespaceNotifications(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	eaaCtx := r.Context().Value(contextKey("appliance-ctx")).(*Context)
	var (
		sub        []NotificationDescriptor
		commonName string
		err        error
		statCode   int
	)

	if err = json.NewDecoder(r.Body).Decode(&sub); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Errf("Namespace Notification Unregistration: %s",
			err.Error())
		return
	}

	commonName = r.TLS.PeerCertificates[0].Subject.CommonName

	vars := mux.Vars(r)

	statCode, err = removeSubscriptionToNamespace(commonName,
		vars["urn.namespace"], sub, eaaCtx)

	if err != nil {
		log.Errf("Namespace Notification Unregistration: %s",
			err.Error())
	}

	w.WriteHeader(statCode)
	log.Debugf("Successfully processed UnsubscribeNamespaceNotifications from"+
		"%s", commonName)
}

// UnsubscribeServiceNotifications implements https API
func UnsubscribeServiceNotifications(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	eaaCtx := r.Context().Value(contextKey("appliance-ctx")).(*Context)
	var (
		sub        []NotificationDescriptor
		commonName string
		err        error
		statCode   int
	)

	if err = json.NewDecoder(r.Body).Decode(&sub); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Errf("Service Notification Unregistration: %s", err.Error())
		return
	}

	commonName = r.TLS.PeerCertificates[0].Subject.CommonName

	vars := mux.Vars(r)

	statCode, err = removeSubscriptionToService(commonName,
		vars["urn.namespace"], vars["urn.id"], sub, eaaCtx)

	if err != nil {
		log.Errf("Service Notification Unregistration: %s",
			err.Error())
	}

	w.WriteHeader(statCode)
	log.Debugf("Successfully processed UnsubscribeServiceNotifications from %s",
		commonName)
}
