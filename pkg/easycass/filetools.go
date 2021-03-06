package easycass

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io/ioutil"
	"strconv"
	"strings"
)

// zipinfo is a simple struct used to store information read from the secure
// connect bundle zip file
type zipinfo struct {
	hostname  string
	port      int
	tlsConfig *tls.Config
	keyspace  string
}

// readZip parses through various files in the creds zip and extracts
// information needed in order to connect to the database
func readZip(zippath string) (*zipinfo, error) {
	r, err := zip.OpenReader(zippath)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	filebytes := make(map[string][]byte)

	// Read contents of files we want and place them into the filebytes map
	for _, f := range r.File {
		switch f.Name {
		case "cert", "key", "ca.crt", "config.json", "cqlshrc":
			reader, err := f.Open()
			if err != nil {
				return nil, err
			}

			buf, err := ioutil.ReadAll(reader)
			if err != nil {
				return nil, err
			}

			filebytes[f.Name] = buf
		}
	}

	// Setup tlsconfig based on the cert, key and ca.crt file contents
	cert, _ := tls.X509KeyPair(filebytes["cert"], filebytes["key"])
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(filebytes["ca.crt"])
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
	}

	// we only need the keyspace from the config.json file
	keyspace, err := readConfigJSON(filebytes["config.json"])
	if err != nil {
		return nil, err
	}

	// get hostname and port from the cqlshrc file (the port in config.json is
	// not the correct one)
	hostname, port, err := readCqlshrc(filebytes["cqlshrc"])
	if err != nil {
		return nil, err
	}

	return &zipinfo{
		hostname:  hostname,
		port:      port,
		tlsConfig: tlsConfig,
		keyspace:  keyspace,
	}, nil
}

func readConfigJSON(buf []byte) (keyspace string, err error) {
	config := struct {
		Keyspace string `json:"keyspace"`
	}{}
	err = json.Unmarshal(buf, &config)
	keyspace = config.Keyspace
	return
}

// readCqlshrc gets the hostname and port from the cqlshrc file.  The file is in
// the toml format, but we just do very basic parsing to find the needed values
// so that we don't have to pull in an extra dependency.
func readCqlshrc(buf []byte) (hostname string, port int, err error) {
	buffer := bytes.NewBuffer(buf)

	scanner := bufio.NewScanner(buffer)
	for scanner.Scan() {
		line := scanner.Text()

		// get hostname
		if strings.HasPrefix(line, "hostname") {
			segments := strings.Split(line, "=")
			if len(segments) != 2 {
				err = errors.New("unable to parse hostname line in cqlshrc")
				return
			}
			hostname = strings.TrimSpace(segments[1])
		}

		// get port
		if strings.HasPrefix(line, "port") {
			segments := strings.Split(line, "=")
			if len(segments) != 2 {
				err = errors.New("unable to parse port line in cqlshrc")
				return
			}
			var port64 int64
			port64, err = strconv.ParseInt(strings.TrimSpace(segments[1]), 10, 64)
			if err != nil {
				return
			}
			port = int(port64)
		}

		// if we have both the hostname and the port, we're done
		if hostname != "" && port > 0 {
			return
		}
	}

	// if we get here, something is wrong--first check if the scanner had problems
	err = scanner.Err()
	if err != nil {
		return
	}

	// if the scanner didn't return an error, set an error depending on what info we didn't get
	switch {
	case hostname != "" && port == 0:
		err = errors.New("unable to read port from cqlshrc")

	case hostname == "" && port > 0:
		err = errors.New("unable to read hostname from cqlshrc")

	case hostname == "" && port == 0:
		err = errors.New("unable to read hostname or port from cqlshrc")
	}
	return
}
