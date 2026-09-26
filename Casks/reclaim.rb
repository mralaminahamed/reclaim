cask "reclaim" do
  arch arm: "arm64", intel: "amd64"

  version "0.3.0"
  # Placeholders -- stamp with the real values from dist/SHA256SUMS on release.
  sha256 arm:   "0000000000000000000000000000000000000000000000000000000000000000",
         intel: "1111111111111111111111111111111111111111111111111111111111111111"

  url "https://github.com/mralaminahamed/reclaim/releases/download/v#{version}/reclaim_#{version}_darwin_#{arch}.tar.gz"
  name "reclaim"
  desc "Process-aware disk cleanup that never deletes what it cannot regenerate"
  homepage "https://github.com/mralaminahamed/reclaim"

  livecheck do
    url :url
    strategy :github_latest
  end

  binary "reclaim"

  uninstall delete: "#{HOMEBREW_PREFIX}/bin/reclaim"

  caveats <<~EOS
    reclaim deletes nothing on its own:
      reclaim clean                 # measure and report
      reclaim clean --apply         # reclaim, after confirming

    Shell completion:
      reclaim completion zsh > "$(brew --prefix)/share/zsh/site-functions/_reclaim"
  EOS
end
