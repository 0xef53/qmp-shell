QMP Shell
---------
[![Build Status](https://travis-ci.org/0xef53/qmp-shell.svg?branch=master)](https://travis-ci.org/0xef53/qmp-shell)

QMP Shell is an interface to communicate with QEMU instances via [QEMU Machine Protocol](http://wiki.qemu.org/QMP).

### How to use

There are two ways to work with the QMP Shell:

1. Command line mode
2. Interactive mode

The first one is good for executing single commands, collecting statistics or fetching other data about the QEMU instances.

The interactive mode can be useful for debugging and experimenting.

Some examples:

1. Bring up the network card `alice_ath0`:

        echo set_link name=alice1 up=true | qmp-shell /var/run/kvm-monitor/alice.qmp

2. Set the password for VNC:

        echo set_password protocol=vnc password=secret | qmp-shell /var/run/kvm-monitor/alice.qmp

3. Get memory balloon information:

        echo query-balloon | qmp-shell /var/run/kvm-monitor/alice.qmp
        {
            "actual": 2.44318208e+08
        }

To work with the HMP commands use flag `-H`. Some examples:

1. Get memory balloon information:

        echo info balloon | qmp-shell -H /var/run/kvm-monitor/alice.qmp
        balloon: actual=233

2. Get the VNC server status:

        echo info vnc | qmp-shell -H /var/run/kvm-monitor/alice.qmp

3. All available commands with short description:

        echo help | qmp-shell -H /var/run/kvm-monitor/alice.qmp

### Installing from source

    mkdir qmp-shell && cd qmp-shell
    export GOPATH=$(pwd); go get -v -tags netgo -ldflags '-s -w' github.com/0xef53/qmp-shell
