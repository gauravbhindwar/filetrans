# Homebrew formula for filetrans
#
# To use this tap:
#   brew tap YOUR_USERNAME/filetrans
#   brew install filetrans
#
# Or install from the formula file directly:
#   brew install --formula packaging/homebrew/filetrans.rb
#
# NOTE: Replace SHA256 hashes after each release by running:
#   shasum -a 256 dist/filetrans_darwin_*

class Filetrans < Formula
  desc "Fast USB-C direct file transfer between laptops"
  homepage "https://github.com/YOUR_USERNAME/filetrans"
  version "0.1.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/YOUR_USERNAME/filetrans/releases/download/v#{version}/filetrans_darwin_arm64"
      sha256 "REPLACE_WITH_SHA256_OF_filetrans_darwin_arm64"
    end
    on_intel do
      url "https://github.com/YOUR_USERNAME/filetrans/releases/download/v#{version}/filetrans_darwin_amd64"
      sha256 "REPLACE_WITH_SHA256_OF_filetrans_darwin_amd64"
    end
  end

  def install
    # The downloaded file is the binary itself (no archive)
    binary = on_arm? ? "filetrans_darwin_arm64" : "filetrans_darwin_amd64"
    # Rename to match the formula name
    File.rename(binary, "filetrans") if File.exist?(binary)
    bin.install "filetrans"
  end

  test do
    assert_match "filetrans", shell_output("#{bin}/filetrans --version")
  end
end
