#!/bin/bash
#
# Setup the files and checkouts on a cluster telemetry machine.
#

set -e

# Install packages.
echo "Installing packages..."
sudo apt-get update
sudo apt-get -y install libosmesa-dev libexpat1-dev:i386 clang-3.6 poppler-utils netpbm python-django libgif-dev lua5.2 libnss3
sudo easy_install -U pip
sudo pip install setuptools --no-use-wheel --upgrade
sudo pip install -U crcmod

sudo apt-get -y --purge remove apache2*
sudo sh -c "echo '* - nofile 500000' >> /etc/security/limits.conf"

# Uninstall openjdk-7 and install openjdk-8. See skbug.com/6975 for context.
sudo apt-get -y --purge remove openjdk-7-jdk openjdk-7-jre openjdk-7-jre-headless
sudo apt-get -y install software-properties-common
sudo add-apt-repository -y ppa:openjdk-r/ppa
sudo apt-get update
sudo apt-get -y install openjdk-8-jdk openjdk-8-jre

# Fix symlinks.
sudo ln -s -f /usr/bin/clang-3.6 /usr/bin/clang
sudo ln -s -f /usr/bin/clang++-3.6 /usr/bin/clang++
sudo ln -s -f /usr/bin/llvm-cov-3.6 /usr/bin/llvm-cov
sudo ln -s -f /usr/bin/llvm-profdata-3.6 /usr/bin/llvm-profdata


echo "Installing Python..."

sudo apt-get -y install autotools-dev blt-dev bzip2 dpkg-dev g++-multilib \
    gcc-multilib libbluetooth-dev libbz2-dev libexpat1-dev libffi-dev libffi6 \
    libffi6-dbg libgdbm-dev libgpm2 libncursesw5-dev libreadline-dev \
    libsqlite3-dev libssl-dev libtinfo-dev mime-support net-tools netbase \
    python-crypto python-mox3 python-pil python-ply quilt tk-dev zlib1g-dev \
    mesa-utils android-tools-adb
# Install Python 2.7.11. See skbug.com/5562 for context.
wget https://www.python.org/ftp/python/2.7.11/Python-2.7.11.tgz
tar xfz Python-2.7.11.tgz
cd Python-2.7.11/
./configure --prefix /usr/local/lib/python2.7.11 --enable-ipv6
make
sudo make install
# Install psutil in Python 2.7.11. See skbug.com/7293 for context.
sudo /usr/local/lib/python2.7.11/bin/python -m ensurepip --upgrade
sudo /usr/local/lib/python2.7.11/bin/pip install psutil httplib2

echo "Checking out depot_tools..."

if [ ! -d "/b/depot_tools" ]; then
  cd /b/
  git clone https://chromium.googlesource.com/chromium/tools/depot_tools.git
  echo 'export PATH=/b/depot_tools:$PATH' >> ~/.bashrc
fi
PATH=$PATH:/b/depot_tools

echo "Checking out Chromium repository..."

mkdir -p /b/storage/chromium
cd /b/storage/chromium
/b/depot_tools/fetch chromium
cd src
git checkout master
/b/depot_tools/gclient sync

echo "Checking out Skia's buildbot and trunk, and PDFium repositories..."

mkdir /b/skia-repo/
cd /b/skia-repo/
cat > .gclient << EOF
solutions = [
  { 'name'        : 'buildbot',
    'url'         : 'https://skia.googlesource.com/buildbot.git',
    'deps_file'   : 'DEPS',
    'managed'     : True,
    'custom_deps' : {
    },
    'safesync_url': '',
  },
  { 'name'        : 'trunk',
    'url'         : 'https://skia.googlesource.com/skia.git',
    'deps_file'   : 'DEPS',
    'managed'     : True,
    'custom_deps' : {
    },
    'safesync_url': '',
  },
  { 'name'        : 'pdfium',
    'url'         : 'https://pdfium.googlesource.com/pdfium.git',
    'deps_file'   : 'DEPS',
    'managed'     : False,
    'custom_deps' : {
    },
    'safesync_url': '',
  },
]
EOF
/b/depot_tools/gclient sync

# Checkout master in the repositories so that we can run "git pull" later.
cd buildbot
git checkout master
cd ../trunk
git checkout master
cd ../pdfium
git checkout master
# Create glog dir.
mkdir /b/storage/glog

# Get access token from metadata.
TOKEN=`curl "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token" -H "Metadata-Flavor: Google" | python -c "import sys, json; print json.load(sys.stdin)['access_token']"`
# Bootstrap Swarming.
mkdir -p /b/s
SWARMING=https://chrome-swarming.appspot.com
HOSTNAME=`hostname`
curl ${SWARMING}/bot_code?bot_id=$HOSTNAME -H "Authorization":"Bearer $TOKEN" -o /b/s/swarming_bot.zip
ln -sf /b/s /b/swarm_slave

echo
echo "The setup script has completed!"
echo
