#######################################################
#
# Control Center Smoke Test
#
# Please add any tests you want executed at the bottom.
#
#######################################################

DIR="$(cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd)"
SERVICED=${DIR}/serviced
IP=$(/sbin/ifconfig eth0 | grep 'inet addr:' | cut -d: -f2 | awk {'print $1'})
HOSTNAME=$(hostname)

succeed() {
    echo ===== SUCCESS =====
    echo $@
    echo ===================
}

fail() {
    echo ====== FAIL ======
    echo $@
    echo ==================
    exit 1
}

# until ubuntu delivers util-linux-2.24, install required nsenter
install_prereqs() {
    if [ -z "$(which nsenter)" ]; then
        echo "nsenter is not installed - installing nsenter"
        # TODO: replace apt-* with yum commands for fedora
        sudo apt-add-repository "deb [ arch=amd64 ] http://apt.zendev.org/apt/ubuntu trusty multiverse"
        sudo apt-get --yes update
        wget -q http://apt.zendev.org/key/zendev_signing_key.pub -O- | sudo apt-key add -
        sudo apt-get --yes install docker-smuggle
        if [ -z "$(which nsenter)" ]; then
            fail "ERROR: nsenter is not installed - serviced attach tests will fail"
        fi
    fi

    local wget_image="zenoss/ubuntu:wget"
    if ! docker inspect "${wget_image}" >/dev/null; then
        docker pull "${wget_image}"
       if ! docker inspect "${wget_image}" >/dev/null; then
            fail "ERROR: docker image "${wget_image}" is not available - wget tests will fail"
       fi
    fi
}

# Add the vhost to /etc/hosts so we can resolve it for the test
add_to_etc_hosts() {
    if [ -z "$(grep -e "^${IP} websvc.${HOSTNAME}" /etc/hosts)" ]; then
        sudo /bin/bash -c "echo ${IP} websvc.${HOSTNAME} >> /etc/hosts"
    fi
}

cleanup() {
    sudo pkill -9 serviced
    docker kill $(docker ps -q)
    sudo rm -rf /tmp/serviced-root/var/isvcs/*
}
trap cleanup EXIT


start_serviced() {
    echo "Starting serviced..."
    sudo GOPATH=${GOPATH} PATH=${PATH} SERVICED_NOREGISTRY="true" ${SERVICED} -master -agent &
    echo "Waiting 120 seconds for serviced to become the leader..."
    retry 180 wget --no-check-certificate http://${HOSTNAME}:443 &>/dev/null
    return $?
}

# Add a host
add_host() {
    HOST_ID=$(${SERVICED} host add "${IP}:4979" default)
    sleep 1
    [ -z "$(${SERVICED} host list ${HOST_ID} 2>/dev/null)" ] && return 1
    return 0
}

add_template() {
    TEMPLATE_ID=$(${SERVICED} template compile ${DIR}/dao/testsvc | ${SERVICED} template add)
    sleep 1
    [ -z "$(${SERVICED} template list ${TEMPLATE_ID})" ] && return 1
    return 0
}

deploy_service() {
    echo "Deploying template id ${TEMPLATE_ID}"
    echo ${SERVICED} service deploy ${TEMPLATE_ID} default testsvc
    SERVICE_ID=$(${SERVICED} template deploy ${TEMPLATE_ID} default testsvc)
    echo " deployed template id ${TEMPLATE_ID} - SERVICE_ID='${SERVICE_ID}'"
    sleep 2
    [ -z "$(${SERVICED} service list ${SERVICE_ID})" ] && return 1
    return 0
}

start_service() {
    ${SERVICED} service start ${SERVICE_ID}
    sleep 10 
    [ -z "${SERVICED} service list ${SERVICE_ID}" ] && return 1
    return 0
}

test_started() {
    for line in $(${SERVICED} service list | tr -cd '\000-\177' | awk '/s[12]/{print $1 ":" $2}'); do
        name=$(echo $line | cut -f1 -d:)
        id=$(echo $line | cut -f2 -d:)
        docker ps --no-trunc | grep "proxy $id" &>/dev/null
        status=$?
        if [ "$status" != "0" ]; then
            echo "Unable to find service {Name:$name ID:$id} container in docker ps"
            if [[ 1 = $TRY_COUNTDOWN ]]; then
                echo "Output of ${SERVICED} service list:"
                ${SERVICED} service list
                echo
                echo "Output of docker ps --no-trunc | grep -v isvcs:"
                docker ps --no-trunc | grep -v isvcs
            fi
            return 1
        fi
    done
    return 0
}

test_vhost() {
    wget --no-check-certificate -qO- https://websvc.${HOSTNAME} &>/dev/null || return 1
    return 0
}

test_assigned_ip() {
    wget ${IP}:1000 -qO- &>/dev/null || return 1
    return 0
}

test_config() {
    wget --no-check-certificate -qO- ${IP}:1000/etc/my.cnf | grep "innodb_buffer_pool_size"  || return 1
    return 0
}

test_dir_config() {
    [ "$(wget --no-check-certificate -qO- https://websvc.${HOSTNAME}/etc/bar.txt)" == "baz" ] || return 1
    return 0
}

test_attached() {
    varx=$(${SERVICED} service attach s1 whoami)
    if [[ "$varx" == "root" ]]; then
        return 0
    fi
    return 1
}

test_port_mapped() {
    echo "${SERVICED} service attach s1 wget -qO- http://localhost:9090/etc/bar.txt"
    varx=`${SERVICED} service attach s1 wget -qO- http://localhost:9090/etc/bar.txt 2>/dev/null | tr -d '\r'| grep -v "^$" | tail -1`
    if [[ "$varx" == "baz" ]]; then
        return 0
    fi
    return 1
}

retry() {
    TIMEOUT=$1
    shift
    COMMAND="$@"
    DURATION=0
    until [ ${DURATION} -ge ${TIMEOUT} ]; do
        TRY_COUNTDOWN=$[${TIMEOUT} - ${DURATION}]
        ${COMMAND}; RESULT=$?; [ ${RESULT} = 0 ] && break
        DURATION=$[$DURATION+1]
        sleep 1
    done
    return ${RESULT}
}

# Force a clean environment
cleanup

# Setup
install_prereqs
add_to_etc_hosts

# Run all the tests
start_serviced             && succeed "Serviced became leader within timeout"    || fail "serviced failed to become the leader within 120 seconds."
add_host                   && succeed "Added host successfully"                  || fail "Unable to add host"
add_template               && succeed "Added template successfully"              || fail "Unable to add template"
deploy_service             && succeed "Deployed service successfully"            || fail "Unable to deploy service"
start_service              && succeed "Started service"                          || fail "Unable to start service"
retry 10 test_started      && succeed "Service containers started"               || fail "Unable to see service containers"

retry 10 test_vhost        && succeed "VHost is up and listening"                || fail "Unable to access service VHost"
retry 10 test_assigned_ip  && succeed "Assigned IP is listening"                 || fail "Unable to access service by assigned IP"
retry 10 test_config       && succeed "Config file was successfully injected"    || fail "Unable to access config file"
retry 10 test_dir_config   && succeed "-CONFIGS- file was successfully injected" || fail "Unable to access -CONFIGS- file"

retry 10 test_attached     && succeed "Attached to container"                    || fail "Unable to attach to container"
retry 10 test_port_mapped  && succeed "Attached and hit imported port correctly" || fail "Unable to connect to endpoint"

# "trap cleanup EXIT", above, will handle cleanup
